"""Pruebas de HU-03: saldo y compra de tokens simulada.

Cada prueba corresponde a un criterio de aceptacion de la historia o a un RNF
que la historia exige (RNF-01 transaccional, RNF-02 idempotencia, RNF-10
ledger) y a HU-12 (el admin no compra).
"""
import threading
import uuid

from sqlalchemy import select

from app.db import SessionLocal
from app.models import LEDGER_COMPLETED, LEDGER_PURCHASE, LedgerEntry
from app.services.billetera import MONTO_MAXIMO_COP, comprar_tokens, saldo_de, tasa
from tests.conftest import requires_db

CLAVE = "clave123"  # la que usa el fixture make_user


def _login(client, user):
    r = client.post("/auth/login", json={"email": user.email, "password": CLAVE})
    assert r.status_code == 200


def _comprar(client, monto_cop, clave=None):
    clave = clave or str(uuid.uuid4())
    return client.post("/billetera/compras", json={"monto_cop": monto_cop}, headers={"Idempotency-Key": clave})


def _movimientos(user_id):
    with SessionLocal() as db:
        return db.scalars(select(LedgerEntry).where(LedgerEntry.user_id == user_id)).all()


# ---------------------------------------------------------------------------
# GET /billetera
# ---------------------------------------------------------------------------
@requires_db
def test_billetera_nueva_tiene_saldo_0_y_la_tasa(client, make_user):
    _login(client, make_user("player"))

    r = client.get("/billetera")
    assert r.status_code == 200
    assert r.json() == {"saldo": 0, "cop_por_token": tasa()}
    assert r.headers["cache-control"] == "no-store"


# ---------------------------------------------------------------------------
# Criterio: el pago se aprueba, se convierte con la tasa y veo el saldo nuevo
# ---------------------------------------------------------------------------
@requires_db
def test_compra_convierte_con_la_tasa_y_devuelve_el_saldo_nuevo(client, make_user):
    user = make_user("player")
    _login(client, user)

    r = _comprar(client, 50 * tasa())
    assert r.status_code == 201
    cuerpo = r.json()
    assert cuerpo["tokens_acreditados"] == 50
    assert cuerpo["saldo"] == 50
    assert client.get("/billetera").json()["saldo"] == 50

    # Una segunda compra (otra clave) suma sobre el saldo anterior.
    assert _comprar(client, 20 * tasa()).json()["saldo"] == 70


# ---------------------------------------------------------------------------
# Criterio: monto <= 0 -> la compra se rechaza (y no se acredita nada)
# ---------------------------------------------------------------------------
@requires_db
def test_monto_cero_o_negativo_se_rechaza(client, make_user):
    user = make_user("player")
    _login(client, user)

    for monto in (0, -tasa()):
        r = _comprar(client, monto)
        assert r.status_code == 422
        assert r.json() == {"detail": "El monto debe ser mayor que 0"}

    assert client.get("/billetera").json()["saldo"] == 0
    assert _movimientos(user.id) == []


@requires_db
def test_monto_que_no_da_tokens_enteros_o_sobre_el_tope_se_rechaza(client, make_user):
    user = make_user("player")
    _login(client, user)

    assert _comprar(client, tasa() + tasa() // 2).status_code == 422  # p. ej. 1.500 con tasa 1.000
    assert _comprar(client, MONTO_MAXIMO_COP + tasa()).status_code == 422
    assert _comprar(client, "mucho").status_code == 422  # no es un numero
    assert _movimientos(user.id) == []


# ---------------------------------------------------------------------------
# Criterio: doble clic o reenvio -> se acredita una sola vez (RNF-02)
# ---------------------------------------------------------------------------
@requires_db
def test_reenvio_con_la_misma_clave_acredita_una_sola_vez(client, make_user):
    user = make_user("player")
    _login(client, user)
    clave = str(uuid.uuid4())

    primera = _comprar(client, 10 * tasa(), clave)
    segunda = _comprar(client, 10 * tasa(), clave)

    assert primera.status_code == 201
    assert segunda.status_code == 200  # reenvio: misma compra, nada nuevo
    assert segunda.json()["compra_id"] == primera.json()["compra_id"]
    assert client.get("/billetera").json()["saldo"] == 10
    assert len(_movimientos(user.id)) == 1


@requires_db
def test_misma_clave_con_otro_monto_responde_409(client, make_user):
    user = make_user("player")
    _login(client, user)
    clave = str(uuid.uuid4())

    assert _comprar(client, 10 * tasa(), clave).status_code == 201
    r = _comprar(client, 99 * tasa(), clave)
    assert r.status_code == 409
    assert client.get("/billetera").json()["saldo"] == 10


@requires_db
def test_la_misma_clave_en_dos_usuarios_no_choca(client, make_user):
    ana, beto = make_user("player"), make_user("player")
    clave = str(uuid.uuid4())

    _login(client, ana)
    assert _comprar(client, 5 * tasa(), clave).status_code == 201
    _login(client, beto)
    assert _comprar(client, 5 * tasa(), clave).status_code == 201

    with SessionLocal() as db:
        assert saldo_de(db, ana.id) == 5
        assert saldo_de(db, beto.id) == 5


@requires_db
def test_dos_peticiones_simultaneas_con_la_misma_clave_acreditan_una_vez(make_user):
    """El caso dificil de RNF-02: las dos pasan el chequeo de "ya existe" a
    la vez. La restriccion unica de la BD decide cual gana."""
    user = make_user("player")
    clave = str(uuid.uuid4())
    barrera = threading.Barrier(2)
    resultados, errores = [], []

    def comprar():
        try:
            with SessionLocal() as db:
                barrera.wait()
                resultados.append(comprar_tokens(db, user.id, 7 * tasa(), clave))
        except Exception as error:  # noqa: BLE001  (se revisa abajo)
            errores.append(error)

    hilos = [threading.Thread(target=comprar) for _ in range(2)]
    for h in hilos:
        h.start()
    for h in hilos:
        h.join()

    assert errores == []
    assert sorted(r.nueva for r in resultados) == [False, True]
    assert resultados[0].movimiento.id == resultados[1].movimiento.id
    with SessionLocal() as db:
        assert saldo_de(db, user.id) == 7
    assert len(_movimientos(user.id)) == 1


@requires_db
def test_sin_idempotency_key_responde_400(client, make_user):
    _login(client, make_user("player"))

    r = client.post("/billetera/compras", json={"monto_cop": 10 * tasa()})
    assert r.status_code == 400
    assert client.get("/billetera").json()["saldo"] == 0


# ---------------------------------------------------------------------------
# Criterio: la compra queda en el ledger con usuario, tipo, monto, fecha y
# estado (RNF-10)
# ---------------------------------------------------------------------------
@requires_db
def test_la_compra_queda_en_el_ledger(client, make_user):
    user = make_user("player")
    _login(client, user)

    compra = _comprar(client, 3 * tasa()).json()

    [movimiento] = _movimientos(user.id)
    assert movimiento.id == compra["compra_id"]
    assert movimiento.user_id == user.id
    assert movimiento.type == LEDGER_PURCHASE
    assert movimiento.tokens_delta == 3
    assert movimiento.status == LEDGER_COMPLETED
    assert movimiento.created_at is not None
    assert movimiento.bet_id is None


# ---------------------------------------------------------------------------
# HU-12: el admin no tiene billetera ni compra. Sin sesion: 401.
# ---------------------------------------------------------------------------
@requires_db
def test_el_admin_no_puede_ver_billetera_ni_comprar(client, make_user):
    admin = make_user("admin")
    _login(client, admin)

    assert client.get("/billetera").status_code == 403
    assert _comprar(client, 10 * tasa()).status_code == 403
    assert _movimientos(admin.id) == []


@requires_db
def test_sin_sesion_responde_401(client):
    assert client.get("/billetera").status_code == 401
    assert _comprar(client, 10 * tasa()).status_code == 401
