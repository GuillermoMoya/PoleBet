"""HU2-21: contrato de las consultas del administrador por el Broker.

Lo define esta HU (el consumidor de Pagos) porque HU2-18 (Admin/Broker, Iván)
aún no existe. Cuando exista, Admin publica mensajes con esta forma y lee las
respuestas con el mismo `correlation_id`. Si Iván elige otro formato, se ajusta
solo aquí y en app/consumer.py; la consulta SQL (services/rentabilidad.py) no
cambia.

Mensaje de consulta (lo publica Admin en la cola de consultas):

    {
      "tipo": "rentabilidad",
      "filtros": {"event_id": "...", "desde": "...", "hasta": "...",
                  "kind": "estatica", "template": "ganador", "top_n": 5}
    }

El `correlation_id` y la cola de respuesta (`reply_to`) viajan en las
PROPIEDADES del mensaje AMQP, no en el cuerpo (es lo estándar en el patrón
petición-respuesta de RabbitMQ).

Mensaje de respuesta (lo publica Pagos en `reply_to`, con el mismo
`correlation_id`):

    {"tipo": "rentabilidad", "ok": true, "datos": {...}}
    {"tipo": "rentabilidad", "ok": false, "error": "kind_invalido"}
"""
import uuid
from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

# Tipos de consulta que entiende el consumidor. HU2-21 implementa "rentabilidad";
# el resto de consultas del admin (HU2-20, HU2-22) añadirán sus tipos aquí.
TIPO_RENTABILIDAD = "rentabilidad"


class FiltrosConsulta(BaseModel):
    """Filtros de RF-29. Todos opcionales."""

    model_config = ConfigDict(extra="forbid")

    event_id: uuid.UUID | None = None
    desde: datetime | None = None
    hasta: datetime | None = None
    kind: Literal["estatica", "dinamica"] | None = None
    template: str | None = Field(default=None, max_length=100)
    top_n: int = Field(default=5, ge=1, le=100)


class ConsultaRentabilidad(BaseModel):
    """Cuerpo del mensaje de consulta que publica Admin."""

    model_config = ConfigDict(extra="forbid")

    tipo: Literal["rentabilidad"] = TIPO_RENTABILIDAD
    filtros: FiltrosConsulta = Field(default_factory=FiltrosConsulta)


class FilaPlantillaOut(BaseModel):
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


class DatosRentabilidad(BaseModel):
    filas: list[FilaPlantillaOut]
    top_ganancia: list[FilaPlantillaOut]
    peor_ganancia: list[FilaPlantillaOut]
    total_apuestas: int
    total_apostado: int
    total_pagado: int
    ganancia_neta: int


class RespuestaConsulta(BaseModel):
    """Lo que Pagos publica en la cola de respuesta."""

    tipo: str
    ok: bool
    datos: DatosRentabilidad | None = None
    error: str | None = None
