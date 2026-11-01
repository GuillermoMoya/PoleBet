"""HU2-21: rentabilidad por tipo y plantilla (consulta del administrador).

La agregación se prueba contra PostgreSQL (requires_db). El handler de la
consulta (procesar_consulta) se prueba sin RabbitMQ, con un doble de la sesión
para los casos de error y con la base real para el camino feliz.
"""
import json
import uuid
from datetime import datetime, timedelta, timezone
from decimal import Decimal

from sqlalchemy import delete

from app.db import SessionLocal
from app.models import (
    BET_INDIVIDUAL,
    BET_LOST,
    BET_TORNEO,
    BET_WON,
    MARKET_DINAMICA,
    MARKET_ESTATICA,
    Bet,
    Event,
    Market,
)
from app.services.consulta_handler import procesar_consulta
from app.services.rentabilidad import FiltrosRentabilidad, KindInvalido, calcular_rentabilidad
from tests.conftest import requires_db


class DatosRent:
    """Crea un evento con mercados y apuestas para medir la rentabilidad."""

    def __init__(self):
        self.event_id = uuid.uuid4()
        self.market_ids: list[uuid.UUID] = []

    def crear_evento(self):
        with SessionLocal() as db:
            db.add(
                Event(
                    id=self.event_id,
                    race_key=f"rent-{uuid.uuid4().hex[:8]}",
                    starts_at=datetime.now(timezone.utc),
                    buy_in=100,
                    initial_chips=1000,
                    commission_pct=Decimal("10"),
                )
            )
            db.commit()
        return self

    def mercado(self, kind, template) -> uuid.UUID:
        mid = uuid.uuid4()
        with SessionLocal() as db:
            db.add(
                Market(
                    id=mid,
                    event_id=self.event_id,
                    kind=kind,
                    template=template,
                    question="¿?",
                    options=[{"opcion": "Sí", "multiplicador": 2}],
                    status="liquidado",
                )
            )
            db.commit()
        self.market_ids.append(mid)
        return mid

    def apuesta(self, market_id, *, mode, amount, multiplier, status, payout, creada=None):
        with SessionLocal() as db:
            db.add(
                Bet(
                    id=uuid.uuid4(),
                    user_id=uuid.uuid4(),
                    market_id=market_id,
                    event_id=self.event_id,
                    mode=mode,
                    option="Sí",
                    amount=amount,
                    multiplier=Decimal(str(multiplier)),
                    status=status,
                    payout=payout,
                    created_at=creada or datetime.now(timezone.utc),
                )
            )
            db.commit()

    def limpiar(self):
        with SessionLocal() as db:
            db.execute(delete(Bet).where(Bet.event_id == self.event_id))
            db.execute(delete(Market).where(Market.event_id == self.event_id))
            db.execute(delete(Event).where(Event.id == self.event_id))
            db.commit()


@requires_db
def test_agrega_por_tipo_y_plantilla_con_ganancia_neta():
    d = DatosRent().crear_evento()
    try:
        ganador = d.mercado(MARKET_ESTATICA, "ganador")
        podio = d.mercado(MARKET_ESTATICA, "podio")
        # ganador: 2 apuestas, 1 acierta (paga 200), 1 pierde. Apostado 200,
        # pagado 200 -> ganancia 0.
        d.apuesta(ganador, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status=BET_WON, payout=200)
        d.apuesta(ganador, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status=BET_LOST, payout=0)
        # podio: 1 apuesta que pierde. Apostado 50, pagado 0 -> ganancia 50.
        d.apuesta(podio, mode=BET_INDIVIDUAL, amount=50, multiplier=3, status=BET_LOST, payout=0)

        with SessionLocal() as db:
            r = calcular_rentabilidad(db, FiltrosRentabilidad())

        por_tpl = {(f.kind, f.template): f for f in r.filas}
        g = por_tpl[(MARKET_ESTATICA, "ganador")]
        assert (g.apuestas, g.aciertos, g.acierto_pct) == (2, 1, 50.0)
        assert (g.apostado, g.pagado, g.ganancia_neta) == (200, 200, 0)
        assert g.mercados_generados == 1
        p = por_tpl[(MARKET_ESTATICA, "podio")]
        assert (p.apuestas, p.apostado, p.pagado, p.ganancia_neta) == (1, 50, 0, 50)
        # Totales.
        assert (r.total_apuestas, r.total_apostado, r.total_pagado, r.ganancia_neta) == (3, 250, 200, 50)
        # Ranking: podio es la más rentable; ganador la menos.
        assert r.top_ganancia[0].template == "podio"
        assert r.peor_ganancia[0].template == "ganador"
    finally:
        d.limpiar()


@requires_db
def test_las_apuestas_de_torneo_no_cuentan_en_la_ganancia():
    d = DatosRent().crear_evento()
    try:
        m = d.mercado(MARKET_DINAMICA, "cambio_lider")
        d.apuesta(m, mode=BET_TORNEO, amount=500, multiplier=2, status=BET_WON, payout=1000)
        d.apuesta(m, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status=BET_LOST, payout=0)
        with SessionLocal() as db:
            r = calcular_rentabilidad(db, FiltrosRentabilidad())
        fila = next(f for f in r.filas if f.template == "cambio_lider")
        # Solo la individual: 1 apuesta, apostado 100, ganancia 100.
        assert (fila.apuestas, fila.apostado, fila.ganancia_neta) == (1, 100, 100)
    finally:
        d.limpiar()


@requires_db
def test_pending_no_cuenta_y_mercado_sin_apuestas_sale_con_ceros():
    d = DatosRent().crear_evento()
    try:
        m = d.mercado(MARKET_ESTATICA, "vuelta_rapida")
        d.apuesta(m, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status="pending", payout=None)
        vacio = d.mercado(MARKET_DINAMICA, "sin_apuestas")  # mercado sin bets
        with SessionLocal() as db:
            r = calcular_rentabilidad(db, FiltrosRentabilidad())
        vr = next(f for f in r.filas if f.template == "vuelta_rapida")
        assert vr.apuestas == 0 and vr.mercados_generados == 1
        sa = next(f for f in r.filas if f.template == "sin_apuestas")
        assert sa.apuestas == 0 and sa.mercados_generados == 1
        # Las plantillas sin apuestas no entran al ranking.
        assert all(f.apuestas > 0 for f in r.top_ganancia)
    finally:
        d.limpiar()


@requires_db
def test_filtros_por_kind_fecha_y_evento():
    d = DatosRent().crear_evento()
    try:
        est = d.mercado(MARKET_ESTATICA, "ganador")
        din = d.mercado(MARKET_DINAMICA, "cambio_lider")
        viejo = datetime.now(timezone.utc) - timedelta(days=10)
        d.apuesta(est, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status=BET_LOST, payout=0, creada=viejo)
        d.apuesta(din, mode=BET_INDIVIDUAL, amount=200, multiplier=2, status=BET_LOST, payout=0)

        with SessionLocal() as db:
            solo_din = calcular_rentabilidad(db, FiltrosRentabilidad(kind=MARKET_DINAMICA))
            assert {f.template for f in solo_din.filas} == {"cambio_lider"}

            recientes = calcular_rentabilidad(
                db, FiltrosRentabilidad(desde=datetime.now(timezone.utc) - timedelta(days=1))
            )
            tpls = {f.template: f.apuestas for f in recientes.filas}
            assert tpls.get("cambio_lider") == 1 and tpls.get("ganador", 0) == 0

            otro = calcular_rentabilidad(db, FiltrosRentabilidad(event_id=uuid.uuid4()))
            assert otro.total_apuestas == 0
    finally:
        d.limpiar()


@requires_db
def test_kind_invalido_se_rechaza():
    import pytest

    with SessionLocal() as db:
        with pytest.raises(KindInvalido):
            calcular_rentabilidad(db, FiltrosRentabilidad(kind="otra_cosa"))


# ---------------------------------------------------------------------------
# Handler de la consulta (lo que corre el consumidor) sin RabbitMQ
# ---------------------------------------------------------------------------
@requires_db
def test_handler_camino_feliz_devuelve_json_ok():
    d = DatosRent().crear_evento()
    try:
        m = d.mercado(MARKET_ESTATICA, "ganador")
        d.apuesta(m, mode=BET_INDIVIDUAL, amount=100, multiplier=2, status=BET_LOST, payout=0)
        cuerpo = json.dumps({"tipo": "rentabilidad", "filtros": {"event_id": str(d.event_id)}}).encode()
        with SessionLocal() as db:
            resp = json.loads(procesar_consulta(db, cuerpo))
        assert resp["ok"] is True and resp["tipo"] == "rentabilidad"
        assert resp["datos"]["total_apostado"] == 100
    finally:
        d.limpiar()


@requires_db
def test_handler_errores_devuelven_ok_false_sin_lanzar():
    with SessionLocal() as db:
        assert json.loads(procesar_consulta(db, b"no es json"))["error"] == "json_invalido"
        assert json.loads(procesar_consulta(db, b'{"tipo":"otra"}'))["error"] == "tipo_no_soportado"
        malo = json.dumps({"tipo": "rentabilidad", "filtros": {"kind": "xyz"}}).encode()
        assert json.loads(procesar_consulta(db, malo))["error"] == "filtros_invalidos"
        extra = json.dumps({"tipo": "rentabilidad", "filtros": {"desconocido": 1}}).encode()
        assert json.loads(procesar_consulta(db, extra))["error"] == "filtros_invalidos"
