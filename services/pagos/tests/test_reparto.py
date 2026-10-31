"""Pruebas de HU2-12: cierre del evento y reparto del botin.

Las primeras pruebas cubren el calculo puro (no necesitan PostgreSQL). Las
demas recorren el flujo real: crear evento, inscribir, largar y cerrar.
"""
import itertools
import random
import threading
import uuid
from datetime import datetime, timedelta, timezone
from decimal import Decimal

from sqlalchemy import func, select

from app.db import SessionLocal
from app.models import LEDGER_PRIZE, EventEntry, LedgerEntry, Wallet
from app.services.eventos import clave_inscripcion
from app.services.reparto import (
    FichasFinales,
    calcular_reparto,
    clave_cierre,
    comision,
    minimo_apostado,
)
from tests.conftest import headers, requires_db

LARGADA = (datetime.now(timezone.utc) + timedelta(minutes=10)).replace(microsecond=0).isoformat()

# UUIDs fijos y ordenados para que los desempates sean predecibles.
U = [uuid.UUID(int=i + 1) for i in range(10)]


def _f(uid, final, apostado=1000):
    return FichasFinales(user_id=uid, chips_final=final, chips_wagered=apostado)


def _premios(rep):
    return {j.user_id: j.prize for j in rep.jugadores}


def _cuadra(rep, pot):
    return rep.prizes_total + rep.commission + rep.residue == pot and rep.residue >= 0


# ---------------------------------------------------------------------------
# Calculo puro
# ---------------------------------------------------------------------------
def test_reparto_basico_50_30_20_sin_residuo():
    # Botin 1000, comision 10 % = 100, repartible 900 -> 450 / 270 / 180.
    rep = calcular_reparto(1000, Decimal("10"), 1000, 50, [_f(U[0], 3000), _f(U[1], 2000), _f(U[2], 1000), _f(U[3], 500)])
    assert rep.commission == 100
    assert _premios(rep) == {U[0]: 450, U[1]: 270, U[2]: 180, U[3]: 0}
    assert rep.residue == 0 and _cuadra(rep, 1000)
    assert [j.position_final for j in rep.jugadores] == [1, 2, 3, 4]


def test_tres_empatados_en_el_primero_dividen_el_50_y_el_siguiente_es_segundo():
    # Ejemplo del enunciado. Repartible 900: 50 % = 450 / 3 = 150 cada uno;
    # el siguiente jugador es el 2.º y recibe el 30 % = 270; nadie toma el 20 %.
    rep = calcular_reparto(1000, Decimal("10"), 1000, 50, [_f(U[0], 2000), _f(U[1], 2000), _f(U[2], 2000), _f(U[3], 900)])
    assert _premios(rep) == {U[0]: 150, U[1]: 150, U[2]: 150, U[3]: 270}
    assert [j.position_final for j in rep.jugadores] == [1, 1, 1, 2]
    # El 20 % del tercer puesto no tiene jugadores: va a la casa.
    assert rep.residue == 180 and _cuadra(rep, 1000)


def test_empate_en_el_segundo_puesto_divide_el_30():
    rep = calcular_reparto(1000, Decimal("10"), 1000, 50, [_f(U[0], 3000), _f(U[1], 2000), _f(U[2], 2000), _f(U[3], 100)])
    # 1.º 450; 2.º 270 / 2 = 135 cada uno; 3.º (U[3]) 180.
    assert _premios(rep) == {U[0]: 450, U[1]: 135, U[2]: 135, U[3]: 180}
    assert _cuadra(rep, 1000)


def test_residuo_del_redondeo_va_a_la_casa():
    # Botin 700, comision 70, repartible 630: 315 / 189 / 126 exactos.
    # Con 3 empatados en el 1.º: 315 / 3 = 105; 2.º 189. Sobra 126.
    rep = calcular_reparto(700, Decimal("10"), 1000, 50, [_f(U[0], 5), _f(U[1], 5), _f(U[2], 5), _f(U[3], 1)])
    assert _premios(rep) == {U[0]: 105, U[1]: 105, U[2]: 105, U[3]: 189}
    # Botin 100: comision 10, repartible 90, 50 % = 45 entre 2 = 22 (sobra 1).
    rep = calcular_reparto(100, Decimal("10"), 1000, 50, [_f(U[0], 5), _f(U[1], 5)])
    assert _premios(rep) == {U[0]: 22, U[1]: 22}
    assert rep.residue == 100 - 10 - 44 and _cuadra(rep, 100)


def test_los_no_elegibles_no_cobran_y_el_podio_se_arma_con_los_elegibles():
    # Minimo 500 apostadas. U[0] tiene mas fichas pero aposto poco: no cobra.
    rep = calcular_reparto(
        1000, Decimal("10"), 1000, 50,
        [_f(U[0], 5000, apostado=499), _f(U[1], 2000, 500), _f(U[2], 1500, 800), _f(U[3], 1000, 600)],
    )
    p = _premios(rep)
    assert p[U[0]] == 0 and not next(j for j in rep.jugadores if j.user_id == U[0]).eligible
    assert (p[U[1]], p[U[2]], p[U[3]]) == (450, 270, 180)
    # El puesto final es el del ranking (todos): U[0] sigue siendo 1.º.
    assert next(j for j in rep.jugadores if j.user_id == U[0]).position_final == 1


def test_sin_elegibles_todo_el_repartible_va_a_la_casa():
    rep = calcular_reparto(400, Decimal("10"), 1000, 50, [_f(u, 1000, apostado=0) for u in U[:4]])
    assert rep.prizes_total == 0 and rep.commission == 40 and rep.residue == 360


def test_minimo_y_comision_redondean_como_el_motor():
    assert minimo_apostado(999, 50) == 500  # techo, igual que fichas/ranking.go
    assert minimo_apostado(1000, 50) == 500
    assert minimo_apostado(1000, 0) == 0
    assert comision(105, Decimal("10")) == 10  # 10,5 se trunca
    assert comision(1000, Decimal("12.5")) == 125


def test_con_minimo_cero_todos_son_elegibles():
    rep = calcular_reparto(400, Decimal("10"), 1000, 0, [_f(u, 100 * (i + 1), apostado=0) for i, u in enumerate(U[:4])])
    assert all(j.eligible for j in rep.jugadores)
    assert rep.prizes_total > 0


def test_invariante_premios_comision_residuo_igual_al_botin():
    # Propiedad (RNF-11) sobre muchos casos aleatorios reproducibles.
    azar = random.Random(20261031)
    for _ in range(500):
        n = azar.randint(1, 10)
        pot = azar.randint(0, 50_000)
        pct = Decimal(azar.choice(["0", "5", "10", "12.5", "33.33"]))
        jugadores = [_f(U[i], azar.choice([0, 100, 100, 500, 900, 2000]), azar.randint(0, 1000)) for i in range(n)]
        rep = calcular_reparto(pot, pct, 1000, 50, jugadores)
        assert _cuadra(rep, pot), (pot, pct, jugadores, rep)
        assert all(j.prize >= 0 for j in rep.jugadores)


def test_el_orden_de_entrada_no_cambia_el_resultado():
    base = [_f(U[0], 2000), _f(U[1], 2000), _f(U[2], 900), _f(U[3], 900), _f(U[4], 10)]
    esperado = _premios(calcular_reparto(1000, Decimal("10"), 1000, 50, base))
    for perm in itertools.islice(itertools.permutations(base), 30):
        assert _premios(calcular_reparto(1000, Decimal("10"), 1000, 50, list(perm))) == esperado


# ---------------------------------------------------------------------------
# Flujo con PostgreSQL
# ---------------------------------------------------------------------------
def _crear(client, fabrica):
    eid = uuid.uuid4()
    fabrica.eventos.append(eid)
    cuerpo = {"event_id": str(eid), "race_key": "2024_monza", "starts_at": LARGADA}
    assert client.post("/internal/events", json=cuerpo, headers=headers()).status_code == 201
    return eid


def _torneo_activo(client, fabrica, n=4, saldo=500):
    """Evento con n inscritos (100 de buy_in cada uno) y ya largado."""
    eid = _crear(client, fabrica)
    jugadores = [fabrica.billetera(balance=saldo) for _ in range(n)]
    for uid in jugadores:
        r = client.post(
            f"/internal/events/{eid}/registrations",
            json={"user_id": str(uid)},
            headers=headers(clave_inscripcion(eid, uid)),
        )
        assert r.status_code == 201, r.text
    assert client.post(f"/internal/events/{eid}/start", headers=headers()).status_code == 200
    return eid, jugadores


def _cerrar(client, eid, players, clave=None):
    return client.post(
        f"/internal/events/{eid}/close",
        json={"players": players},
        headers=headers(clave or clave_cierre(eid)),
    )


def _saldo(uid):
    with SessionLocal() as db:
        return db.scalar(select(Wallet.balance).where(Wallet.user_id == uid))


@requires_db
def test_cerrar_reparte_paga_y_deja_resultado_en_event_entries(client, fabrica):
    eid, js = _torneo_activo(client, fabrica)  # botin 400; saldos 400 tras el buy_in
    players = [
        {"user_id": str(js[0]), "chips_final": 3000, "chips_wagered": 900},
        {"user_id": str(js[1]), "chips_final": 2000, "chips_wagered": 600},
        {"user_id": str(js[2]), "chips_final": 1000, "chips_wagered": 500},
        {"user_id": str(js[3]), "chips_final": 4000, "chips_wagered": 100},  # no elegible
    ]
    r = _cerrar(client, eid, players)
    assert r.status_code == 201, r.text
    body = r.json()
    # Comision 40, repartible 360 -> 180 / 108 / 72.
    assert (body["pot"], body["commission"], body["prizes_total"], body["residue"]) == (400, 40, 360, 0)
    assert (body["status"], body["tournament_status"], body["nueva"]) == ("finalizado", "repartido", True)
    assert (_saldo(js[0]), _saldo(js[1]), _saldo(js[2]), _saldo(js[3])) == (580, 508, 472, 400)

    with SessionLocal() as db:
        e = db.get(EventEntry, (eid, js[3]))
        assert (e.chips_final, e.chips_wagered, e.position_final, e.eligible, e.prize) == (4000, 100, 1, False, 0)
        e = db.get(EventEntry, (eid, js[0]))
        assert (e.position_final, e.eligible, e.prize) == (2, True, 180)
        premios = db.scalar(
            select(func.count()).select_from(LedgerEntry).where(LedgerEntry.event_id == eid, LedgerEntry.type == LEDGER_PRIZE)
        )
        assert premios == 3


@requires_db
def test_repetir_el_cierre_no_paga_doble(client, fabrica):
    eid, js = _torneo_activo(client, fabrica)
    players = [{"user_id": str(u), "chips_final": 1000 + i, "chips_wagered": 500} for i, u in enumerate(js)]
    assert _cerrar(client, eid, players).status_code == 201
    saldos = [_saldo(u) for u in js]

    otra = _cerrar(client, eid, players)
    assert otra.status_code == 200 and otra.json()["nueva"] is False
    # Sin fichas (el Motor ya limpio Redis): tambien devuelve lo guardado.
    vacia = _cerrar(client, eid, [])
    assert vacia.status_code == 200 and vacia.json()["prizes_total"] == otra.json()["prizes_total"]
    assert [_saldo(u) for u in js] == saldos

    # Mismo evento con otras fichas: se rechaza.
    distinto = [{**p, "chips_final": 1} for p in players]
    assert _cerrar(client, eid, distinto).status_code == 409


@requires_db
def test_cierres_simultaneos_pagan_una_sola_vez(client, fabrica):
    eid, js = _torneo_activo(client, fabrica)
    players = [{"user_id": str(u), "chips_final": 1000 * (i + 1), "chips_wagered": 500} for i, u in enumerate(js)]
    codigos = []

    def cerrar():
        codigos.append(_cerrar(client, eid, players).status_code)

    hilos = [threading.Thread(target=cerrar) for _ in range(4)]
    for h in hilos:
        h.start()
    for h in hilos:
        h.join()
    assert sorted(codigos) == [200, 200, 200, 201]
    with SessionLocal() as db:
        pagado = db.scalar(
            select(func.coalesce(func.sum(LedgerEntry.tokens_delta), 0)).where(
                LedgerEntry.event_id == eid, LedgerEntry.type == LEDGER_PRIZE
            )
        )
    assert pagado == 360


@requires_db
def test_jugadores_que_no_coinciden_con_los_inscritos_se_rechazan(client, fabrica):
    eid, js = _torneo_activo(client, fabrica)
    faltan = [{"user_id": str(u), "chips_final": 1000, "chips_wagered": 500} for u in js[:3]]
    assert _cerrar(client, eid, faltan).status_code == 409
    extra = faltan + [{"user_id": str(uuid.uuid4()), "chips_final": 1, "chips_wagered": 1}]
    assert _cerrar(client, eid, extra).status_code == 409
    repetido = faltan + [faltan[0]]
    assert _cerrar(client, eid, repetido).status_code == 409


@requires_db
def test_no_se_cierra_un_evento_sin_largar_y_se_valida_la_clave(client, fabrica):
    eid = _crear(client, fabrica)
    assert _cerrar(client, eid, []).status_code == 409
    assert _cerrar(client, eid, [], clave="otra-clave").status_code == 400
    assert _cerrar(client, eid, [], clave=f"event-close:{uuid.uuid4()}").status_code == 400
    otro = uuid.uuid4()
    assert _cerrar(client, otro, [], clave=clave_cierre(otro)).status_code == 404
    assert client.post(f"/internal/events/{eid}/close", json={"players": []}).status_code == 401


@requires_db
def test_torneo_cancelado_solo_finaliza_la_carrera(client, fabrica):
    eid = _crear(client, fabrica)
    uid = fabrica.billetera(balance=500)
    client.post(f"/internal/events/{eid}/registrations", json={"user_id": str(uid)}, headers=headers(clave_inscripcion(eid, uid)))
    assert client.post(f"/internal/events/{eid}/start", headers=headers()).json()["tournament_status"] == "cancelado"

    r = _cerrar(client, eid, [])
    assert r.status_code == 201, r.text
    assert (r.json()["status"], r.json()["tournament_status"], r.json()["prizes_total"]) == ("finalizado", "cancelado", 0)
    assert _cerrar(client, eid, []).status_code == 200
    assert _saldo(uid) == 500  # el buy_in ya se habia devuelto en la largada
