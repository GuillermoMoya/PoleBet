"""POST /internal/purchases (HU2-04, contrato del Orquestador §3.3)."""
import uuid

from sqlalchemy import select

from app.db import SessionLocal
from app.models import LEDGER_PURCHASE, LedgerEntry
from app.services.billetera import saldo_de, tasa
from tests.conftest import headers, requires_db


def _comprar(client, user_id, monto_cop, clave=None, token_ok=True):
    clave = clave if clave is not None else f"purchase:{user_id}:{uuid.uuid4()}"
    h = headers(clave) if token_ok else {"Idempotency-Key": clave}
    return client.post("/internal/purchases", json={"user_id": str(user_id), "monto_cop": monto_cop}, headers=h)


def _saldo(user_id):
    with SessionLocal() as db:
        return saldo_de(db, user_id)


def _movimientos(user_id):
    with SessionLocal() as db:
        return db.scalars(select(LedgerEntry).where(LedgerEntry.user_id == user_id)).all()


@requires_db
def test_compra_nueva_acredita_y_guarda_la_clave_del_contrato(client, fabrica):
    user_id = fabrica.billetera(balance=1250)
    clave = f"purchase:{user_id}:{uuid.uuid4()}"

    r = _comprar(client, user_id, 50 * tasa(), clave)
    assert r.status_code == 201
    cuerpo = r.json()
    assert (cuerpo["tokens_acreditados"], cuerpo["saldo"]) == (50, 1300)
    assert set(cuerpo) == {"compra_id", "tokens_acreditados", "saldo", "creada_en"}

    [movimiento] = _movimientos(user_id)
    assert (movimiento.id, movimiento.type, movimiento.idempotency_key) == (cuerpo["compra_id"], LEDGER_PURCHASE, clave)


@requires_db
def test_reenvio_responde_200_sin_acreditar_y_otro_monto_409(client, fabrica):
    user_id = fabrica.billetera()
    clave = f"purchase:{user_id}:{uuid.uuid4()}"

    primera = _comprar(client, user_id, 10 * tasa(), clave)
    reenvio = _comprar(client, user_id, 10 * tasa(), clave)
    otro_monto = _comprar(client, user_id, 99 * tasa(), clave)

    assert (primera.status_code, reenvio.status_code, otro_monto.status_code) == (201, 200, 409)
    assert reenvio.json()["compra_id"] == primera.json()["compra_id"]
    assert _saldo(user_id) == 10
    assert len(_movimientos(user_id)) == 1


@requires_db
def test_monto_invalido_responde_422_con_el_motivo(client, fabrica):
    user_id = fabrica.billetera()

    r = _comprar(client, user_id, tasa() + tasa() // 2)
    assert r.status_code == 422
    assert r.json()["detail"].startswith("El monto debe ser múltiplo de")
    assert _comprar(client, user_id, 0).status_code == 422
    assert _saldo(user_id) == 0
    assert _movimientos(user_id) == []


@requires_db
def test_la_clave_debe_ser_purchase_del_mismo_usuario(client, fabrica):
    user_id = fabrica.billetera()
    otro = uuid.uuid4()
    for clave in ("", "abc", f"purchase:{user_id}:", f"purchase:{otro}:abc", f"compra:{user_id}:abc", f"purchase:{user_id}:" + "x" * 250):
        assert _comprar(client, user_id, 10 * tasa(), clave).status_code == 400, clave
    assert _movimientos(user_id) == []


@requires_db
def test_sin_billetera_responde_404(client, fabrica):
    user_id = fabrica.usuario_sin_billetera()
    assert _comprar(client, user_id, 10 * tasa()).status_code == 404
    assert _movimientos(user_id) == []


@requires_db
def test_sin_token_de_servicio_responde_401(client, fabrica):
    user_id = fabrica.billetera()
    assert _comprar(client, user_id, 10 * tasa(), token_ok=False).status_code == 401
    assert _saldo(user_id) == 0
