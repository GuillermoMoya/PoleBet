"""Logica de la compra de tokens contra wallets (T2-03, HU2-04).

El endpoint POST /internal/purchases se prueba en test_purchases.py.
"""
import threading
import uuid

import pytest
from sqlalchemy import select

from app.db import SessionLocal
from app.models import LEDGER_COMPLETED, LEDGER_PURCHASE, LedgerEntry
from app.services.billetera import (
    MONTO_MAXIMO_COP,
    BilleteraNoExiste,
    ClaveReutilizada,
    MontoInvalido,
    comprar_tokens,
    saldo_de,
    tasa,
)
from tests.conftest import requires_db


def _clave(user_id, clave=None):
    return f"purchase:{user_id}:{clave or uuid.uuid4()}"


def _comprar(user_id, monto_cop, clave=None):
    with SessionLocal() as db:
        return comprar_tokens(db, user_id, monto_cop, _clave(user_id, clave))


def _movimientos(user_id):
    with SessionLocal() as db:
        return db.scalars(select(LedgerEntry).where(LedgerEntry.user_id == user_id)).all()


def _saldo(user_id):
    with SessionLocal() as db:
        return saldo_de(db, user_id)


@requires_db
def test_compra_convierte_con_la_tasa_y_acredita_en_wallets(fabrica):
    user_id = fabrica.billetera()

    resultado = _comprar(user_id, 50 * tasa())
    assert resultado.nueva
    assert resultado.saldo == 50
    assert _comprar(user_id, 20 * tasa()).saldo == 70
    assert _saldo(user_id) == 70


@requires_db
def test_la_compra_queda_en_el_ledger(fabrica):
    user_id = fabrica.billetera()

    resultado = _comprar(user_id, 3 * tasa())

    [movimiento] = _movimientos(user_id)
    assert movimiento.id == resultado.movimiento.id
    assert movimiento.type == LEDGER_PURCHASE
    assert movimiento.tokens_delta == 3
    assert movimiento.status == LEDGER_COMPLETED
    assert movimiento.bet_id is None and movimiento.event_id is None
    assert movimiento.created_at is not None


@requires_db
def test_montos_invalidos_no_acreditan(fabrica):
    user_id = fabrica.billetera()

    for monto in (0, -tasa(), tasa() + tasa() // 2, MONTO_MAXIMO_COP + tasa()):
        with pytest.raises(MontoInvalido):
            _comprar(user_id, monto)
    assert _saldo(user_id) == 0
    assert _movimientos(user_id) == []


@requires_db
def test_sin_billetera_no_acredita_ni_deja_movimiento(fabrica):
    user_id = fabrica.usuario_sin_billetera()

    with pytest.raises(BilleteraNoExiste):
        _comprar(user_id, 10 * tasa())
    assert _movimientos(user_id) == []


@requires_db
def test_reenvio_con_la_misma_clave_acredita_una_sola_vez(fabrica):
    user_id = fabrica.billetera()
    clave = str(uuid.uuid4())

    primera = _comprar(user_id, 10 * tasa(), clave)
    segunda = _comprar(user_id, 10 * tasa(), clave)

    assert (primera.nueva, segunda.nueva) == (True, False)
    assert segunda.movimiento.id == primera.movimiento.id
    assert _saldo(user_id) == 10
    with pytest.raises(ClaveReutilizada):
        _comprar(user_id, 99 * tasa(), clave)
    assert _saldo(user_id) == 10


@requires_db
def test_la_misma_clave_en_dos_usuarios_no_choca(fabrica):
    ana, beto = fabrica.billetera(), fabrica.billetera()
    clave = str(uuid.uuid4())

    assert _comprar(ana, 5 * tasa(), clave).nueva
    assert _comprar(beto, 5 * tasa(), clave).nueva
    assert (_saldo(ana), _saldo(beto)) == (5, 5)


@requires_db
def test_dos_compras_simultaneas_con_la_misma_clave_acreditan_una_vez(fabrica):
    user_id = fabrica.billetera()
    clave = str(uuid.uuid4())
    barrera = threading.Barrier(2)
    resultados, errores = [], []

    def comprar():
        try:
            with SessionLocal() as db:
                barrera.wait()
                resultados.append(comprar_tokens(db, user_id, 7 * tasa(), _clave(user_id, clave)))
        except Exception as error:  # noqa: BLE001  (se revisa abajo)
            errores.append(error)

    hilos = [threading.Thread(target=comprar) for _ in range(2)]
    for h in hilos:
        h.start()
    for h in hilos:
        h.join()

    assert errores == []
    assert sorted(r.nueva for r in resultados) == [False, True]
    assert _saldo(user_id) == 7
    assert len(_movimientos(user_id)) == 1
