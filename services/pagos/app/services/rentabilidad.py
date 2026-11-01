"""HU2-21 (RF-26): rentabilidad de las apuestas por tipo y plantilla.

La responde SOLO Pagos, porque aquí viven los eventos, los mercados (con su
`kind` y su `template`), las apuestas y el ledger. El administrador la pide por
el Broker (ver app/consumer.py); esta capa es la consulta pura sobre la base y
no sabe nada de RabbitMQ.

Qué calcula, para estáticas y dinámicas y, dentro de cada tipo, por plantilla:

  - apuestas: cuántas se hicieron (status won/lost; las pending no cuentan);
  - aciertos y % de acierto;
  - apostado: suma de `amount`;
  - pagado: suma de `payout` de las ganadoras;
  - ganancia_neta: apostado − pagado (lo que gana o pierde la plataforma);
  - multiplicador_promedio: promedio de `bets.multiplier`.

Y el ranking de las N plantillas con más y con menos ganancia neta, con cuántas
veces se generó cada plantilla (mercados creados con ese template).

Alcance de la ganancia (decisión documentada en hu2-21-rentabilidad.md): la
"ganancia de la plataforma" solo es en tokens, así que las métricas de dinero
(apostado, pagado, ganancia) se calculan sobre las apuestas INDIVIDUALES. Las de
torneo se cuentan aparte (participación), porque mueven fichas, no tokens. RF-26
habla de rentabilidad de la casa, que es un concepto de tokens.
"""
import uuid
from dataclasses import dataclass, field
from datetime import datetime
from decimal import Decimal

from sqlalchemy import case, func, select
from sqlalchemy.orm import Session

from app.models import (
    BET_INDIVIDUAL,
    BET_WON,
    MARKET_DINAMICA,
    MARKET_ESTATICA,
    Bet,
    Market,
)

# Tipos de mercado válidos para filtrar; el resto se rechaza.
TIPOS_VALIDOS = (MARKET_ESTATICA, MARKET_DINAMICA)


@dataclass(frozen=True)
class FiltrosRentabilidad:
    """Filtros de RF-29. Todos opcionales; None = sin filtrar por ese campo."""

    event_id: uuid.UUID | None = None
    desde: datetime | None = None  # created_at de la apuesta >= desde
    hasta: datetime | None = None  # created_at de la apuesta <= hasta
    kind: str | None = None  # "estatica" | "dinamica"
    template: str | None = None
    top_n: int = 5  # tamaño del ranking de plantillas


class KindInvalido(ValueError):
    """El filtro kind no es estatica ni dinamica."""


@dataclass
class FilaPlantilla:
    kind: str
    template: str
    apuestas: int
    aciertos: int
    acierto_pct: float
    apostado: int
    pagado: int
    ganancia_neta: int
    multiplicador_promedio: float
    mercados_generados: int


@dataclass
class ResumenRentabilidad:
    filas: list[FilaPlantilla] = field(default_factory=list)
    top_ganancia: list[FilaPlantilla] = field(default_factory=list)
    peor_ganancia: list[FilaPlantilla] = field(default_factory=list)
    # Totales de control (individual): apuestas, apostado, pagado, ganancia.
    total_apuestas: int = 0
    total_apostado: int = 0
    total_pagado: int = 0
    ganancia_neta: int = 0


def _aplicar_filtros(stmt, filtros: FiltrosRentabilidad):
    """Aplica los filtros comunes de RF-29 a un SELECT sobre bets JOIN markets."""
    if filtros.event_id is not None:
        stmt = stmt.where(Bet.event_id == filtros.event_id)
    if filtros.desde is not None:
        stmt = stmt.where(Bet.created_at >= filtros.desde)
    if filtros.hasta is not None:
        stmt = stmt.where(Bet.created_at <= filtros.hasta)
    if filtros.kind is not None:
        stmt = stmt.where(Market.kind == filtros.kind)
    if filtros.template is not None:
        stmt = stmt.where(Market.template == filtros.template)
    return stmt


def _contar_mercados(db: Session, filtros: FiltrosRentabilidad) -> dict[tuple[str, str], int]:
    """Cuántos mercados se generaron por (kind, template). Para RF-26 ('cuántas
    veces se generó cada plantilla'), independientemente de que recibieran
    apuestas."""
    stmt = select(Market.kind, Market.template, func.count(Market.id)).group_by(Market.kind, Market.template)
    if filtros.event_id is not None:
        stmt = stmt.where(Market.event_id == filtros.event_id)
    if filtros.kind is not None:
        stmt = stmt.where(Market.kind == filtros.kind)
    if filtros.template is not None:
        stmt = stmt.where(Market.template == filtros.template)
    return {(k, t): n for k, t, n in db.execute(stmt).all()}


def calcular_rentabilidad(db: Session, filtros: FiltrosRentabilidad) -> ResumenRentabilidad:
    """Agrega las apuestas individuales resueltas por (kind, template)."""
    if filtros.kind is not None and filtros.kind not in TIPOS_VALIDOS:
        raise KindInvalido(filtros.kind)

    pagado_expr = func.coalesce(func.sum(Bet.payout), 0)
    apostado_expr = func.coalesce(func.sum(Bet.amount), 0)
    ganadas_expr = func.sum(case((Bet.status == BET_WON, 1), else_=0))

    stmt = (
        select(
            Market.kind,
            Market.template,
            func.count(Bet.id).label("apuestas"),
            ganadas_expr.label("aciertos"),
            apostado_expr.label("apostado"),
            pagado_expr.label("pagado"),
            func.coalesce(func.avg(Bet.multiplier), 0).label("mult_prom"),
        )
        .join(Market, Bet.market_id == Market.id)
        # Solo individuales resueltas: la ganancia de la casa es en tokens y una
        # apuesta pending todavía no tiene resultado.
        .where(Bet.mode == BET_INDIVIDUAL, Bet.status != "pending")
        .group_by(Market.kind, Market.template)
    )
    stmt = _aplicar_filtros(stmt, filtros)

    mercados = _contar_mercados(db, filtros)
    filas: list[FilaPlantilla] = []
    for kind, template, apuestas, aciertos, apostado, pagado, mult_prom in db.execute(stmt).all():
        apuestas = int(apuestas)
        aciertos = int(aciertos)
        apostado = int(apostado)
        pagado = int(pagado)
        filas.append(
            FilaPlantilla(
                kind=kind,
                template=template,
                apuestas=apuestas,
                aciertos=aciertos,
                acierto_pct=round(aciertos / apuestas * 100, 2) if apuestas else 0.0,
                apostado=apostado,
                pagado=pagado,
                ganancia_neta=apostado - pagado,
                multiplicador_promedio=round(float(mult_prom), 2),
                mercados_generados=mercados.get((kind, template), 0),
            )
        )

    # Plantillas que se generaron pero no recibieron apuestas individuales
    # resueltas: salen en el detalle con ceros (siguen siendo informativas).
    con_apuestas = {(f.kind, f.template) for f in filas}
    for (kind, template), n in mercados.items():
        if (kind, template) not in con_apuestas:
            filas.append(
                FilaPlantilla(kind, template, 0, 0, 0.0, 0, 0, 0, 0.0, n)
            )

    # Orden estable: por tipo y luego por plantilla.
    filas.sort(key=lambda f: (f.kind, f.template))

    # Ranking por ganancia neta. Solo entran las plantillas con apuestas (un
    # ranking de "más/menos ganancia" sobre plantillas sin apuestas no informa).
    con_datos = [f for f in filas if f.apuestas > 0]
    por_ganancia = sorted(con_datos, key=lambda f: f.ganancia_neta, reverse=True)
    n = max(1, filtros.top_n)

    return ResumenRentabilidad(
        filas=filas,
        top_ganancia=por_ganancia[:n],
        peor_ganancia=sorted(con_datos, key=lambda f: f.ganancia_neta)[:n],
        total_apuestas=sum(f.apuestas for f in filas),
        total_apostado=sum(f.apostado for f in filas),
        total_pagado=sum(f.pagado for f in filas),
        ganancia_neta=sum(f.ganancia_neta for f in filas),
    )
