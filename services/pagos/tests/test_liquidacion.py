"""Pruebas focalizadas de débito y liquidación interna HU-07."""
import threading
import uuid
from decimal import Decimal

from sqlalchemy import select, update

from app.db import SessionLocal
from app.core.internal_auth import token_servicio_valido
from app.models import (
    BET_LOST,
    BET_PENDING,
    BET_WON,
    LEDGER_BET_DEBIT,
    LEDGER_BET_SETTLEMENT,
    Bet,
    LedgerEntry,
    User,
)
from app.schemas.internal import BetSettlementIn
from app.services.liquidacion import BetDebitData, debitar_apuesta, liquidar_apuesta
from tests.conftest import requires_db

TOKEN = "token-interno-de-pruebas-para-hu07"


def test_internal_service_token_is_required_and_compared():
    assert token_servicio_valido(TOKEN)
    for token in (None, "incorrecto", "carácter-no-ascii-que-falla"):
        assert not token_servicio_valido(token)


def test_settlement_contract_validates_result_points_and_unknown_fields():
    request = BetSettlementIn.model_validate(
        {
            "bet_id": str(uuid.uuid4()),
            "result": "won",
            "points": "10.5",
            "idempotency_key": "bet-settlement:00000000-0000-4000-8000-000000000000",
        }
    )
    assert request.points == Decimal("10.5")
    for invalid in (
        {"result": "pending", "points": "0"},
        {"result": "lost", "points": "-1"},
        {"result": "lost", "points": "1", "extra": True},
    ):
        payload = {
            "bet_id": str(uuid.uuid4()),
            "idempotency_key": "bet-settlement:invalid",
            **invalid,
        }
        try:
            BetSettlementIn.model_validate(payload)
        except ValueError:
            continue
        raise AssertionError(f"se aceptó contrato inválido: {invalid}")


def _headers(clave=None, token=TOKEN):
    headers = {"Idempotency-Key": clave} if clave else {}
    if token:
        headers["X-Internal-Service-Token"] = token
    return headers


def _debitar(client, user, amount, bet_id=None):
    bet_id = bet_id or uuid.uuid4()
    clave = f"bet-debit:{bet_id}"
    response = client.post(
        "/internal/debit",
        json={
            "bet_id": str(bet_id),
            "user_id": str(user.id),
            "market_id": "market-demo",
            "option": "VER",
            "amount": amount,
            "idempotency_key": clave,
        },
        headers=_headers(clave),
    )
    return response, bet_id


def _liquidar(client, bet_id, result, points):
    clave = f"bet-settlement:{bet_id}"
    return client.post(
        "/internal/settlements",
        json={
            "bet_id": str(bet_id),
            "result": result,
            "points": str(points),
            "idempotency_key": clave,
        },
        headers=_headers(clave),
    )


@requires_db
def test_internal_debit_and_settlement_record_win_loss_and_ledger(client, make_user):
    user = make_user()
    with SessionLocal() as db:
        db.execute(update(User).where(User.id == user.id).values(balance=200))
        db.commit()

    winner_debit, winner_id = _debitar(client, user, 100)
    loser_debit, loser_id = _debitar(client, user, 20)
    assert winner_debit.status_code == 201
    assert loser_debit.status_code == 201

    won = _liquidar(client, winner_id, "won", Decimal("60"))
    lost = _liquidar(client, loser_id, "lost", Decimal("0"))
    assert won.status_code == 201
    assert Decimal(str(won.json()["points_awarded"])) == Decimal("60")
    assert lost.status_code == 201
    assert Decimal(str(lost.json()["points_awarded"])) == Decimal("0")

    with SessionLocal() as db:
        refreshed = db.get(User, user.id)
        bets = db.scalars(select(Bet).where(Bet.user_id == user.id)).all()
        ledger = db.scalars(select(LedgerEntry).where(LedgerEntry.user_id == user.id)).all()
        assert refreshed.balance == 80
        assert refreshed.points == Decimal("60.0")
        assert {bet.status for bet in bets} == {BET_WON, BET_LOST}
        assert sum(entry.type == LEDGER_BET_DEBIT for entry in ledger) == 2
        settlements = [entry for entry in ledger if entry.type == LEDGER_BET_SETTLEMENT]
        assert len(settlements) == 2
        assert sum(entry.points_delta for entry in settlements) == Decimal("60.0")
        assert all(entry.tokens_delta == 0 for entry in settlements)


@requires_db
def test_settlement_retry_returns_existing_effect_without_double_award(client, make_user):
    user = make_user()
    with SessionLocal() as db:
        db.execute(update(User).where(User.id == user.id).values(balance=100))
        db.commit()
    debit, bet_id = _debitar(client, user, 100)
    assert debit.status_code == 201

    first = _liquidar(client, bet_id, "won", Decimal("60"))
    retry = _liquidar(client, bet_id, "won", Decimal("60"))
    assert first.status_code == 201
    assert retry.status_code == 200
    assert retry.json()["nueva"] is False

    with SessionLocal() as db:
        assert db.get(User, user.id).points == Decimal("60.0")
        movements = db.scalars(
            select(LedgerEntry).where(
                LedgerEntry.bet_id == bet_id,
                LedgerEntry.type == LEDGER_BET_SETTLEMENT,
            )
        ).all()
        assert len(movements) == 1


@requires_db
def test_failed_remote_response_can_be_retried_with_same_idempotency_key(client, make_user):
    user = make_user()
    with SessionLocal() as db:
        db.execute(update(User).where(User.id == user.id).values(balance=10))
        db.commit()
    debit, bet_id = _debitar(client, user, 10)
    assert debit.status_code == 201

    # Simula que Pagos confirmó y Motor perdió la respuesta antes de escribir Mongo.
    confirmed = _liquidar(client, bet_id, "lost", Decimal("0"))
    retry = _liquidar(client, bet_id, "lost", Decimal("0"))
    assert confirmed.status_code == 201
    assert retry.status_code == 200
    assert retry.json()["status"] == BET_LOST

    with SessionLocal() as db:
        assert db.get(User, user.id).points == Decimal("0.0")
        movements = db.scalars(
            select(LedgerEntry).where(
                LedgerEntry.bet_id == bet_id,
                LedgerEntry.type == LEDGER_BET_SETTLEMENT,
            )
        ).all()
        assert len(movements) == 1


@requires_db
def test_invalid_points_and_missing_internal_auth_do_not_change_bet(client, make_user):
    user = make_user()
    with SessionLocal() as db:
        db.execute(update(User).where(User.id == user.id).values(balance=100))
        db.commit()
    debit, bet_id = _debitar(client, user, 100)
    assert debit.status_code == 201

    unauthorized = client.post(
        "/internal/settlements",
        json={
            "bet_id": str(bet_id),
            "result": "won",
            "points": "60",
            "idempotency_key": f"bet-settlement:{bet_id}",
        },
    )
    invalid = _liquidar(client, bet_id, "won", Decimal("59"))
    assert unauthorized.status_code == 401
    assert invalid.status_code == 422

    with SessionLocal() as db:
        assert db.get(Bet, bet_id).status == BET_PENDING
        assert db.get(User, user.id).points == Decimal("0.0")
        assert db.scalar(
            select(LedgerEntry.id).where(
                LedgerEntry.bet_id == bet_id,
                LedgerEntry.type == LEDGER_BET_SETTLEMENT,
            )
        ) is None


@requires_db
def test_concurrent_settlement_awards_points_once(make_user):
    user = make_user()
    bet_id = uuid.uuid4()
    debit_key = f"bet-debit:{bet_id}"
    with SessionLocal() as db:
        db.execute(update(User).where(User.id == user.id).values(balance=50))
        debitar_apuesta(
            db,
            BetDebitData(
                bet_id=bet_id,
                user_id=user.id,
                market_id="market-demo",
                option="NOR",
                amount=50,
                idempotency_key=debit_key,
            ),
        )

    clave = f"bet-settlement:{bet_id}"
    barrera = threading.Barrier(2)
    resultados, errores = [], []

    def liquidar():
        try:
            with SessionLocal() as db:
                barrera.wait()
                resultados.append(
                    liquidar_apuesta(db, bet_id, BET_WON, Decimal("35"), clave)
                )
        except Exception as error:  # revisado abajo
            errores.append(error)

    hilos = [threading.Thread(target=liquidar) for _ in range(2)]
    for hilo in hilos:
        hilo.start()
    for hilo in hilos:
        hilo.join()

    assert errores == []
    assert sorted(resultado.nueva for resultado in resultados) == [False, True]
    with SessionLocal() as db:
        assert db.get(User, user.id).points == Decimal("35.0")
        movimientos = db.scalars(
            select(LedgerEntry).where(
                LedgerEntry.bet_id == bet_id,
                LedgerEntry.type == LEDGER_BET_SETTLEMENT,
            )
        ).all()
        assert len(movimientos) == 1
