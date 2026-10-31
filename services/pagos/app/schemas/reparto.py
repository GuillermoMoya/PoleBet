"""Contrato REST interno de HU2-12 (Motor -> Pagos): cierre del evento y reparto."""
import uuid

from pydantic import BaseModel, ConfigDict, Field

MAX_FICHAS = 2**53  # cabe en bigint y en el int64 del Motor


class FichasJugadorIn(BaseModel):
    model_config = ConfigDict(extra="forbid")

    user_id: uuid.UUID
    chips_final: int = Field(ge=0, le=MAX_FICHAS)  # saldo de fichas al terminar (Redis)
    chips_wagered: int = Field(ge=0, le=MAX_FICHAS)  # fichas apostadas en todo el torneo


class EventCloseIn(BaseModel):
    """Fichas finales de cada inscrito. Vacia = el torneo fue cancelado o solo se confirma el cierre."""

    model_config = ConfigDict(extra="forbid")

    players: list[FichasJugadorIn] = Field(default_factory=list, max_length=10_000)


class ResultadoJugadorOut(BaseModel):
    user_id: uuid.UUID
    position_final: int
    chips_final: int
    chips_wagered: int
    eligible: bool
    prize: int  # tokens del botin acreditados (0 si no entra al podio)


class EventCloseOut(BaseModel):
    event_id: uuid.UUID
    status: str
    tournament_status: str
    pot: int
    commission: int
    prizes_total: int
    residue: int  # truncamientos y puestos sin elegibles; va a la casa
    players: list[ResultadoJugadorOut]
    nueva: bool
