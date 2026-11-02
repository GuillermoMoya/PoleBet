"""Pruebas de HU2-06 (parte Pagos): eventos e inscripcion al torneo."""
import threading
import uuid
from datetime import datetime, timedelta, timezone

from sqlalchemy import select

from app.core.config import settings
from app.db import SessionLocal
from app.models import LEDGER_BUY_IN, LEDGER_REFUND, LedgerEntry, Wallet
from app.services.eventos import InscripcionCerrada, clave_inscripcion, inscribir
from tests.conftest import headers, requires_db

LARGADA = (datetime.now(timezone.utc) + timedelta(minutes=10)).replace(microsecond=0).isoformat()


def _crear(client, fabrica, **extra):
    eid = uuid.uuid4()
    fabrica.eventos.append(eid)
    cuerpo = {"event_id": str(eid), "race_key": "2024_monza", "starts_at": LARGADA, **extra}
    r = client.post("/internal/events", json=cuerpo, headers=headers())
    assert r.status_code == 201, r.text
    return eid


def _saldo(uid):
    with SessionLocal() as db:
        return db.scalar(select(Wallet.balance).where(Wallet.user_id == uid))


def _inscribir(client, eid, uid, clave=None):
    return client.post(
        f"/internal/events/{eid}/registrations",
        json={"user_id": str(uid)},
        headers=headers(clave or clave_inscripcion(eid, uid)),
    )


def _largar(client, eid):
    return client.post(f"/internal/events/{eid}/start", headers=headers())


# ---------------------------------------------------------------------------
# Crear evento
# ---------------------------------------------------------------------------
@requires_db
def test_crear_evento_usa_la_configuracion_y_es_idempotente(client, fabrica):
    eid = _crear(client, fabrica)
    e = client.get(f"/internal/events/{eid}", headers=headers()).json()
    assert (e["status"], e["tournament_status"], e["buy_in"], e["initial_chips"], e["commission_pct"], e["pot"]) == (
        "programado", "abierto", 100, 1000, 10.0, 0
    )
    cuerpo = {"event_id": str(eid), "race_key": "2024_monza", "starts_at": LARGADA}
    assert client.post("/internal/events", json=cuerpo, headers=headers()).status_code == 200
    distinto = {**cuerpo, "race_key": "2024_spa"}
    assert client.post("/internal/events", json=distinto, headers=headers()).status_code == 409


@requires_db
def test_rutas_internas_exigen_token_de_servicio(client):
    eid = uuid.uuid4()
    assert client.get(f"/internal/events/{eid}").status_code == 401
    assert client.post(f"/internal/events/{eid}/start").status_code == 401


@requires_db
def test_el_buy_in_debe_ser_positivo_pero_no_tiene_minimo_fijo(client, fabrica):
    cuerpo = {"event_id": str(uuid.uuid4()), "race_key": "x", "starts_at": LARGADA, "buy_in": 0}
    assert client.post("/internal/events", json=cuerpo, headers=headers()).status_code == 422
    eid = _crear(client, fabrica, buy_in=50)
    assert client.get(f"/internal/events/{eid}", headers=headers()).json()["buy_in"] == 50


# ---------------------------------------------------------------------------
# Inscribirse
# ---------------------------------------------------------------------------
@requires_db
def test_inscribirse_cobra_suma_al_botin_y_registra_buy_in(client, fabrica):
    eid = _crear(client, fabrica)
    uid = fabrica.billetera(balance=500)

    r = _inscribir(client, eid, uid)

    assert r.status_code == 201, r.text
    assert (r.json()["saldo"], r.json()["pot"], r.json()["initial_chips"], r.json()["refunded"]) == (400, 100, 1000, False)
    with SessionLocal() as db:
        mov = db.scalar(select(LedgerEntry).where(LedgerEntry.idempotency_key == clave_inscripcion(eid, uid)))
        assert (mov.type, mov.tokens_delta, mov.event_id) == (LEDGER_BUY_IN, -100, eid)
    consulta = client.get(f"/internal/events/{eid}/registrations/{uid}", headers=headers())
    assert consulta.status_code == 200 and consulta.json()["tournament_status"] == "abierto"


@requires_db
def test_inscribirse_dos_veces_no_cobra_doble(client, fabrica):
    eid = _crear(client, fabrica)
    uid = fabrica.billetera(balance=500)

    assert _inscribir(client, eid, uid).status_code == 201
    otra = _inscribir(client, eid, uid)

    assert otra.status_code == 200 and otra.json()["nueva"] is False
    assert _saldo(uid) == 400
    assert client.get(f"/internal/events/{eid}", headers=headers()).json()["pot"] == 100


@requires_db
def test_inscripciones_simultaneas_del_mismo_jugador_cobran_una_vez(client, fabrica):
    eid = _crear(client, fabrica)
    uid = fabrica.billetera(balance=500)
    resultados = []

    def intentar():
        with SessionLocal() as db:
            resultados.append(inscribir(db, eid, uid).nueva)

    hilos = [threading.Thread(target=intentar) for _ in range(5)]
    for h in hilos:
        h.start()
    for h in hilos:
        h.join()

    assert sorted(resultados) == [False, False, False, False, True]
    assert _saldo(uid) == 400


@requires_db
def test_errores_de_inscripcion(client, fabrica):
    eid = _crear(client, fabrica)
    pobre = fabrica.billetera(balance=99)

    assert _inscribir(client, eid, pobre).status_code == 422
    assert _saldo(pobre) == 99
    assert _inscribir(client, eid, pobre, clave="otra-clave").status_code == 400
    assert _inscribir(client, uuid.uuid4(), pobre).status_code == 404
    # Sin billetera: no hay saldo -> 422. El rol lo valida el Orquestador.
    assert _inscribir(client, eid, fabrica.usuario_sin_billetera()).status_code == 422
    assert client.get(f"/internal/events/{eid}/registrations/{pobre}", headers=headers()).status_code == 404


# ---------------------------------------------------------------------------
# Largada
# ---------------------------------------------------------------------------
@requires_db
def test_con_menos_de_4_inscritos_se_cancela_el_torneo_y_se_devuelve_todo(client, fabrica):
    eid = _crear(client, fabrica)
    jugadores = [fabrica.billetera(balance=300) for _ in range(3)]
    for uid in jugadores:
        assert _inscribir(client, eid, uid).status_code == 201

    r = _largar(client, eid).json()

    assert (r["status"], r["tournament_status"], r["pot"], r["players"], r["refunded"]) == ("en_curso", "cancelado", 0, [], 3)
    assert [_saldo(u) for u in jugadores] == [300, 300, 300]
    with SessionLocal() as db:
        devoluciones = db.scalars(select(LedgerEntry).where(LedgerEntry.event_id == eid, LedgerEntry.type == LEDGER_REFUND)).all()
        assert len(devoluciones) == 3 and all(d.tokens_delta == 100 for d in devoluciones)
    # Las inscripciones quedan como historial y se ven como devueltas.
    assert client.get(f"/internal/events/{eid}", headers=headers()).json()["registered"] == 3
    assert client.get(f"/internal/events/{eid}/registrations/{jugadores[0]}", headers=headers()).json()["refunded"] is True

    # Repetir la largada no devuelve otra vez.
    otra = _largar(client, eid).json()
    assert (otra["nueva"], otra["refunded"]) == (False, 3)
    assert [_saldo(u) for u in jugadores] == [300, 300, 300]


@requires_db
def test_con_4_inscritos_el_torneo_queda_activo_y_se_cierran_las_inscripciones(client, fabrica):
    eid = _crear(client, fabrica)
    jugadores = [fabrica.billetera(balance=300) for _ in range(4)]
    for uid in jugadores:
        _inscribir(client, eid, uid)

    r = _largar(client, eid).json()

    assert (r["status"], r["tournament_status"], r["pot"], r["initial_chips"]) == ("en_curso", "activo", 400, 1000)
    assert sorted(r["players"]) == sorted(str(u) for u in jugadores)
    tarde = fabrica.billetera(balance=300)
    assert _inscribir(client, eid, tarde).status_code == 409
    assert _saldo(tarde) == 300


@requires_db
def test_la_largada_y_una_inscripcion_no_se_cruzan(client, fabrica, monkeypatch):
    """Con el evento bloqueado, o la inscripcion entra antes y cuenta, o llega tarde y se rechaza."""
    monkeypatch.setattr(settings, "event_min_players", 2)
    eid = _crear(client, fabrica)
    uid = fabrica.billetera(balance=300)
    estado = {}

    def largar():
        estado["largada"] = _largar(client, eid).json()

    def inscribirse():
        with SessionLocal() as db:
            try:
                inscribir(db, eid, uid)
            except InscripcionCerrada:
                pass

    hilos = [threading.Thread(target=largar), threading.Thread(target=inscribirse)]
    for h in hilos:
        h.start()
    for h in hilos:
        h.join()

    # Un solo jugador con minimo 2: si entro se le devolvio; si no entro, nunca se cobro.
    assert _saldo(uid) == 300
    assert estado["largada"]["tournament_status"] == "cancelado"
