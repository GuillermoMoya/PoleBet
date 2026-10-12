"""Operaciones internas de apuesta; saldo, estado y ledger son atómicos."""
import uuid
from dataclasses import dataclass
from decimal import Decimal

from sqlalchemy import select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.models import (
    BET_LOST,
    BET_PENDING,
    BET_WON,
    LEDGER_BET_DEBIT,
    LEDGER_BET_SETTLEMENT,
    LEDGER_COMPLETED,
    Bet,
    LedgerEntry,
    User,
)


class ApuestaNoExiste(Exception):
    """No se encontró la apuesta solicitada."""


class ClaveReutilizada(Exception):
    """La clave de idempotencia ya identifica otro contenido."""


class SaldoInsuficiente(Exception):
    """El usuario no tiene saldo suficiente para la apuesta."""


class UsuarioNoExiste(Exception):
    """No se encontró el usuario de la apuesta."""


class PuntosIncorrectos(Exception):
    """Los puntos solicitados no coinciden con la regla de la demo."""


@dataclass(frozen=True)
class ResultadoDebito:
    apuesta: Bet
    saldo: int
    nueva: bool


@dataclass(frozen=True)
class ResultadoLiquidacion:
    apuesta: Bet
    movimiento: LedgerEntry
    puntos_totales: Decimal
    nueva: bool


def _debito_existente(
    db: Session,
    movimiento: LedgerEntry,
    datos: "BetDebitData",
) -> ResultadoDebito:
    apuesta = db.get(Bet, datos.bet_id)
    if (
        movimiento.type != LEDGER_BET_DEBIT
        or movimiento.status != LEDGER_COMPLETED
        or movimiento.bet_id != datos.bet_id
        or movimiento.user_id != datos.user_id
        or movimiento.tokens_delta != -datos.amount
        or apuesta is None
        or apuesta.user_id != datos.user_id
        or apuesta.market_id != datos.market_id
        or apuesta.option != datos.option
        or apuesta.amount != datos.amount
    ):
        raise ClaveReutilizada()
    saldo = db.scalar(select(User.balance).where(User.id == datos.user_id))
    return ResultadoDebito(apuesta=apuesta, saldo=saldo, nueva=False)


@dataclass(frozen=True)
class BetDebitData:
    bet_id: uuid.UUID
    user_id: uuid.UUID
    market_id: str
    option: str
    amount: int
    idempotency_key: str


def debitar_apuesta(db: Session, datos: BetDebitData) -> ResultadoDebito:
    previo = db.scalar(
        select(LedgerEntry).where(LedgerEntry.idempotency_key == datos.idempotency_key)
    )
    if previo is not None:
        return _debito_existente(db, previo, datos)

    user = db.get(User, datos.user_id)
    if user is None:
        raise UsuarioNoExiste()

    saldo = db.execute(
        update(User)
        .where(User.id == datos.user_id, User.balance >= datos.amount)
        .values(balance=User.balance - datos.amount)
        .returning(User.balance)
    ).scalar_one_or_none()
    if saldo is None:
        raise SaldoInsuficiente()

    apuesta = Bet(
        id=datos.bet_id,
        user_id=datos.user_id,
        market_id=datos.market_id,
        option=datos.option,
        amount=datos.amount,
        status=BET_PENDING,
    )
    db.add(apuesta)
    try:
        db.flush()
        movimiento = LedgerEntry(
            user_id=datos.user_id,
            bet_id=datos.bet_id,
            type=LEDGER_BET_DEBIT,
            tokens_delta=-datos.amount,
            status=LEDGER_COMPLETED,
            idempotency_key=datos.idempotency_key,
        )
        db.add(movimiento)
        db.commit()
    except IntegrityError:
        db.rollback()
        previo = db.scalar(
            select(LedgerEntry).where(
                LedgerEntry.idempotency_key == datos.idempotency_key
            )
        )
        if previo is not None:
            return _debito_existente(db, previo, datos)
        if db.get(Bet, datos.bet_id) is not None:
            raise ClaveReutilizada() from None
        raise
    db.refresh(apuesta)
    return ResultadoDebito(apuesta=apuesta, saldo=saldo, nueva=True)


def puntos_por_apuesta(monto: int) -> Decimal:
    return Decimal("10") + Decimal(monto) * Decimal("0.5")


def liquidar_apuesta(
    db: Session,
    bet_id: uuid.UUID,
    resultado: str,
    puntos: Decimal,
    idempotency_key: str,
) -> ResultadoLiquidacion:
    if resultado not in (BET_WON, BET_LOST):
        raise PuntosIncorrectos()
    apuesta = db.scalar(select(Bet).where(Bet.id == bet_id).with_for_update())
    if apuesta is None:
        raise ApuestaNoExiste()
    esperado = puntos_por_apuesta(apuesta.amount) if resultado == BET_WON else Decimal("0")
    if puntos != esperado:
        raise PuntosIncorrectos()

    previo = db.scalar(
        select(LedgerEntry).where(LedgerEntry.idempotency_key == idempotency_key)
    )
    if previo is not None:
        if (
            previo.type != LEDGER_BET_SETTLEMENT
            or previo.status != LEDGER_COMPLETED
            or previo.bet_id != bet_id
            or previo.user_id != apuesta.user_id
            or previo.points_delta != esperado
            or previo.tokens_delta != 0
            or apuesta.status != resultado
        ):
            raise ClaveReutilizada()
        total = db.scalar(select(User.points).where(User.id == apuesta.user_id))
        return ResultadoLiquidacion(apuesta, previo, total, nueva=False)

    if apuesta.status != BET_PENDING:
        raise ClaveReutilizada()

    movimiento = LedgerEntry(
        user_id=apuesta.user_id,
        bet_id=bet_id,
        type=LEDGER_BET_SETTLEMENT,
        tokens_delta=0,
        points_delta=esperado,
        status=LEDGER_COMPLETED,
        idempotency_key=idempotency_key,
    )
    apuesta.status = resultado
    db.add(movimiento)
    if esperado:
        db.execute(
            update(User)
            .where(User.id == apuesta.user_id)
            .values(points=User.points + esperado)
        )
    db.commit()
    db.refresh(apuesta)
    db.refresh(movimiento)
    total = db.scalar(select(User.points).where(User.id == apuesta.user_id))
    return ResultadoLiquidacion(apuesta, movimiento, total, nueva=True)
