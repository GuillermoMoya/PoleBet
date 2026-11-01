"""Contratos REST internos de HU2-06 (Motor y Orquestador -> Pagos).

Nombres del modelo de datos del P2 (ver documentation/orquestador-api.md).
"""
import uuid
from datetime import datetime
from decimal import Decimal
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

EstadoCarrera = Literal["programado", "en_curso", "finalizado"]
EstadoTorneo = Literal["abierto", "activo", "cancelado", "repartido"]


class _Entrada(BaseModel):
    model_config = ConfigDict(extra="forbid")


class EventCreateIn(_Entrada):
    """Crear evento al arrancar la simulacion. Lo omitido usa la configuracion.

    event_id lo genera quien crea el evento: repetir la llamada con el mismo id
    no crea otro (idempotencia).
    """

    event_id: uuid.UUID
    race_key: str = Field(min_length=1, max_length=100)
    starts_at: datetime  # hora programada de la largada (con zona horaria)
    buy_in: int | None = Field(default=None, gt=0, le=1_000_000)
    initial_chips: int | None = Field(default=None, gt=0, le=1_000_000)
    commission_pct: Decimal | None = Field(default=None, ge=0, le=100, max_digits=5, decimal_places=2)


class EventOut(BaseModel):
    event_id: uuid.UUID
    race_key: str
    status: EstadoCarrera
    tournament_status: EstadoTorneo
    buy_in: int
    initial_chips: int
    commission_pct: float
    pot: int
    registered: int  # filas en event_entries (incluye las de un torneo cancelado)
    starts_at: datetime
    ended_at: datetime | None


class RegistrationIn(_Entrada):
    user_id: uuid.UUID


class RegistrationOut(BaseModel):
    event_id: uuid.UUID
    user_id: uuid.UUID
    tournament_status: EstadoTorneo
    refunded: bool  # inscrito y torneo cancelado: se le devolvio el buy_in
    buy_in: int
    initial_chips: int
    pot: int
    saldo: int | None = None  # solo al inscribirse: PoleCoins que le quedan
    nueva: bool = False


class EventStartOut(BaseModel):
    event_id: uuid.UUID
    status: EstadoCarrera
    tournament_status: EstadoTorneo
    pot: int
    initial_chips: int
    players: list[uuid.UUID]  # inscritos del torneo activo: el Motor les asigna las fichas en Redis
    refunded: int
    nueva: bool
