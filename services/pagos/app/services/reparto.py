"""HU2-12: cierre del evento y reparto del botin del torneo (RF-16).

Al terminar la carrera, el Motor envia las fichas finales de cada inscrito
(viven en Redis, HU2-11) y Pagos, dueño del dinero, calcula y paga el reparto
en una sola transaccion:

  - Comision de la casa: piso(botin x commission_pct / 100).
  - Repartible = botin - comision. Se reparte 50 / 30 / 20 % entre los 3
    primeros puestos ELEGIBLES. Elegible = aposto al menos el
    EVENT_MIN_WAGER_PCT de las fichas iniciales (techo, igual que el Motor).
  - Empates: cada grupo empatado ocupa un solo puesto y divide su porcentaje en
    partes iguales; el siguiente grupo toma el puesto que sigue (1,1,2).
  - Los premios son tokens enteros: cada parte se trunca. Lo que sobra
    (truncamientos y puestos sin jugadores elegibles) es el residuo y va a la
    casa. Por construccion: premios + comision + residuo == botin (RNF-11).

Idempotencia (RNF-12): el cierre bloquea la fila del evento (FOR UPDATE) y
deja tournament_status = repartido. Repetirlo con los mismos datos devuelve lo
guardado sin pagar de nuevo; con datos distintos se rechaza. Cada premio lleva
ademas su clave unica en el ledger (event-prize:{evento}:{usuario}).

La comision y el residuo no van al ledger (ledger_entries exige un user_id y la
casa no tiene billetera): se derivan de events.pot y de los premios pagados.
"""
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import ROUND_DOWN, Decimal

from sqlalchemy import select, update
from sqlalchemy.orm import Session

from app.core.config import settings
from app.models import (
    EVENT_EN_CURSO,
    EVENT_FINALIZADO,
    EVENT_PROGRAMADO,
    LEDGER_COMPLETED,
    LEDGER_PRIZE,
    TORNEO_ACTIVO,
    TORNEO_CANCELADO,
    TORNEO_REPARTIDO,
    Event,
    EventEntry,
    LedgerEntry,
    Wallet,
)

# Porcentaje del repartible para el 1.º, 2.º y 3.º puesto elegible.
PORCENTAJES_PODIO: tuple[int, ...] = (50, 30, 20)


class EventoNoExiste(Exception):
    pass


class EventoNoLargado(Exception):
    """El evento sigue programado: no hay torneo que cerrar."""


class InscritosNoCoinciden(Exception):
    """Las fichas enviadas no corresponden exactamente a los inscritos del evento."""


class CierreDistinto(Exception):
    """El evento ya se repartio con otras fichas finales."""


class BilleteraNoExiste(Exception):
    """Un ganador no tiene billetera donde acreditar el premio."""


def clave_cierre(event_id: uuid.UUID) -> str:
    return f"event-close:{event_id}"


def clave_premio(event_id: uuid.UUID, user_id: uuid.UUID) -> str:
    return f"event-prize:{event_id}:{user_id}"


# ---------------------------------------------------------------------------
# Calculo puro del reparto (sin base de datos)
# ---------------------------------------------------------------------------
@dataclass(frozen=True)
class FichasFinales:
    user_id: uuid.UUID
    chips_final: int
    chips_wagered: int


@dataclass(frozen=True)
class ResultadoJugador:
    user_id: uuid.UUID
    chips_final: int
    chips_wagered: int
    position_final: int  # puesto denso entre TODOS los inscritos, por fichas
    eligible: bool
    prize: int


@dataclass(frozen=True)
class Reparto:
    jugadores: list[ResultadoJugador]
    commission: int
    prizes_total: int
    residue: int


def minimo_apostado(fichas_iniciales: int, pct: int) -> int:
    """Fichas que hay que apostar para ser elegible: techo de fichas x pct / 100."""
    if fichas_iniciales <= 0 or pct <= 0:
        return 0
    return -(-fichas_iniciales * pct // 100)


def comision(pot: int, commission_pct: Decimal) -> int:
    """Comision de la casa en tokens enteros (se trunca la fraccion)."""
    valor = Decimal(pot) * Decimal(commission_pct) / Decimal(100)
    return int(valor.to_integral_value(rounding=ROUND_DOWN))


def _puestos_densos(fichas: list[int]) -> list[int]:
    """Puesto denso para una lista ya ordenada de mayor a menor (1,1,2,...)."""
    puestos: list[int] = []
    for i, f in enumerate(fichas):
        if i == 0:
            puestos.append(1)
        elif f == fichas[i - 1]:
            puestos.append(puestos[-1])
        else:
            puestos.append(puestos[-1] + 1)
    return puestos


def calcular_reparto(
    pot: int,
    commission_pct: Decimal,
    fichas_iniciales: int,
    min_wager_pct: int,
    jugadores: list[FichasFinales],
    porcentajes: tuple[int, ...] = PORCENTAJES_PODIO,
) -> Reparto:
    """Calcula el reparto completo. Funcion pura: mismas entradas, mismo resultado.

    El orden es por fichas finales (mayor primero) y, a igualdad, por user_id
    solo para que el resultado sea estable; el desempate no da ventaja porque
    los empatados comparten puesto y premio.
    """
    minimo = minimo_apostado(fichas_iniciales, min_wager_pct)
    ordenados = sorted(jugadores, key=lambda j: (-j.chips_final, str(j.user_id)))

    # Puesto final que vio el jugador en el ranking: entre todos los inscritos.
    puesto_general = dict(zip((j.user_id for j in ordenados), _puestos_densos([j.chips_final for j in ordenados])))

    # Puesto de reparto: solo entre los elegibles.
    # Con minimo 0 (EVENT_MIN_WAGER_PCT=0) todos son elegibles.
    elegibles = [j for j in ordenados if j.chips_wagered >= minimo]
    puesto_reparto = _puestos_densos([j.chips_final for j in elegibles])

    com = comision(pot, commission_pct)
    repartible = pot - com

    # Cuantos jugadores hay en cada puesto de reparto (para dividir el %).
    por_puesto: dict[int, int] = {}
    for p in puesto_reparto:
        por_puesto[p] = por_puesto.get(p, 0) + 1

    premios: dict[uuid.UUID, int] = {}
    for j, p in zip(elegibles, puesto_reparto):
        if p <= len(porcentajes):
            # Division entera exacta: repartible x % / (100 x empatados).
            premios[j.user_id] = repartible * porcentajes[p - 1] // (100 * por_puesto[p])
        else:
            premios[j.user_id] = 0

    resultado = [
        ResultadoJugador(
            user_id=j.user_id,
            chips_final=j.chips_final,
            chips_wagered=j.chips_wagered,
            position_final=puesto_general[j.user_id],
            eligible=j.user_id in premios,
            prize=premios.get(j.user_id, 0),
        )
        for j in ordenados
    ]
    total = sum(r.prize for r in resultado)
    return Reparto(jugadores=resultado, commission=com, prizes_total=total, residue=pot - com - total)


# ---------------------------------------------------------------------------
# Cierre del evento (lo llama el Motor al terminar la carrera)
# ---------------------------------------------------------------------------
@dataclass(frozen=True)
class ResultadoCierre:
    evento: Event
    reparto: Reparto
    nueva: bool


def _reparto_guardado(evento: Event, entradas: list[EventEntry]) -> Reparto:
    """Reconstruye el reparto desde lo persistido (para respuestas idempotentes)."""
    jugadores = sorted(
        (
            ResultadoJugador(
                user_id=e.user_id,
                chips_final=e.chips_final or 0,
                chips_wagered=e.chips_wagered or 0,
                position_final=e.position_final or 0,
                eligible=bool(e.eligible),
                prize=e.prize or 0,
            )
            for e in entradas
        ),
        key=lambda r: (r.position_final, str(r.user_id)),
    )
    com = comision(evento.pot, evento.commission_pct)
    total = sum(j.prize for j in jugadores)
    return Reparto(jugadores=jugadores, commission=com, prizes_total=total, residue=evento.pot - com - total)


def _finalizar_carrera(evento: Event) -> None:
    evento.status = EVENT_FINALIZADO
    if evento.ended_at is None:
        evento.ended_at = datetime.now(timezone.utc)


def cerrar_evento(db: Session, event_id: uuid.UUID, fichas: list[FichasFinales]) -> ResultadoCierre:
    """Cierra el evento y reparte el botin una sola vez (RF-16, RNF-11, RNF-12)."""
    # Bloquear el evento: serializa cierres concurrentes y la largada (HU2-06).
    evento = db.scalar(select(Event).where(Event.id == event_id).with_for_update())
    if evento is None:
        raise EventoNoExiste()

    entradas = list(db.scalars(select(EventEntry).where(EventEntry.event_id == event_id)))

    # Reintento de un cierre ya hecho: se devuelve lo guardado sin pagar.
    if evento.tournament_status == TORNEO_REPARTIDO:
        guardado = _reparto_guardado(evento, entradas)
        db.commit()  # suelta el bloqueo
        # Sin fichas = el Motor ya limpio Redis y solo confirma el cierre.
        if fichas:
            enviado = {f.user_id: (f.chips_final, f.chips_wagered) for f in fichas}
            persistido = {j.user_id: (j.chips_final, j.chips_wagered) for j in guardado.jugadores}
            if enviado != persistido:
                raise CierreDistinto()
        return ResultadoCierre(evento, guardado, nueva=False)

    if evento.status == EVENT_PROGRAMADO:
        db.rollback()
        raise EventoNoLargado()

    # Torneo cancelado en la largada (HU2-06): el botin ya se devolvio; solo
    # se cierra la carrera. No hay fichas que repartir.
    if evento.tournament_status == TORNEO_CANCELADO:
        nueva = evento.status != EVENT_FINALIZADO
        _finalizar_carrera(evento)
        db.commit()
        db.refresh(evento)
        return ResultadoCierre(evento, Reparto([], 0, 0, evento.pot), nueva=nueva)

    if evento.tournament_status != TORNEO_ACTIVO or evento.status != EVENT_EN_CURSO:
        db.rollback()
        raise EventoNoLargado()

    # Integridad: las fichas deben ser exactamente las de los inscritos.
    inscritos = {e.user_id: e for e in entradas}
    enviados = [f.user_id for f in fichas]
    if len(set(enviados)) != len(enviados) or set(enviados) != set(inscritos):
        db.rollback()
        raise InscritosNoCoinciden()

    reparto = calcular_reparto(
        pot=evento.pot,
        commission_pct=evento.commission_pct,
        fichas_iniciales=evento.initial_chips,
        min_wager_pct=settings.event_min_wager_pct,
        jugadores=fichas,
    )

    for r in reparto.jugadores:
        entrada = inscritos[r.user_id]
        entrada.chips_final = r.chips_final
        entrada.chips_wagered = r.chips_wagered
        entrada.position_final = r.position_final
        entrada.eligible = r.eligible
        entrada.prize = r.prize
        if r.prize > 0:
            acreditado = db.execute(
                update(Wallet)
                .where(Wallet.user_id == r.user_id)
                .values(balance=Wallet.balance + r.prize)
                .returning(Wallet.balance)
            ).scalar_one_or_none()
            if acreditado is None:
                db.rollback()
                raise BilleteraNoExiste()
            db.add(
                LedgerEntry(
                    user_id=r.user_id,
                    event_id=event_id,
                    type=LEDGER_PRIZE,
                    tokens_delta=r.prize,
                    status=LEDGER_COMPLETED,
                    idempotency_key=clave_premio(event_id, r.user_id),
                )
            )

    evento.tournament_status = TORNEO_REPARTIDO
    _finalizar_carrera(evento)
    db.commit()
    db.refresh(evento)
    return ResultadoCierre(evento, reparto, nueva=True)
