"""HU2-21: procesar una consulta del administrador (sin RabbitMQ).

Separar esto del bucle AMQP (app/consumer.py) permite probar la lógica con un
mensaje de bytes y una sesión de base, sin levantar un broker.

Toma el cuerpo del mensaje (bytes JSON), ejecuta la consulta y devuelve los
bytes de la respuesta. Nunca lanza: una consulta malformada o un error de la
base se devuelven como una respuesta {ok: false, error}, para que el admin
reciba siempre algo y la CLI no se quede colgada esperando (RF-30).
"""
import json
import logging

from pydantic import ValidationError
from sqlalchemy.orm import Session

from app.schemas.consultas import (
    TIPO_RENTABILIDAD,
    ConsultaRentabilidad,
    DatosRentabilidad,
    FilaPlantillaOut,
    RespuestaConsulta,
)
from app.services.rentabilidad import (
    FiltrosRentabilidad,
    KindInvalido,
    calcular_rentabilidad,
)

log = logging.getLogger("pagos.consulta")


def _a_salida(f) -> FilaPlantillaOut:
    return FilaPlantillaOut(
        kind=f.kind,
        template=f.template,
        apuestas=f.apuestas,
        aciertos=f.aciertos,
        acierto_pct=f.acierto_pct,
        apostado=f.apostado,
        pagado=f.pagado,
        ganancia_neta=f.ganancia_neta,
        multiplicador_promedio=f.multiplicador_promedio,
        mercados_generados=f.mercados_generados,
    )


def _respuesta(resp: RespuestaConsulta) -> bytes:
    return resp.model_dump_json().encode("utf-8")


def procesar_consulta(db: Session, cuerpo: bytes) -> bytes:
    """Procesa un mensaje de consulta y devuelve los bytes de la respuesta."""
    try:
        crudo = json.loads(cuerpo)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return _respuesta(RespuestaConsulta(tipo="desconocido", ok=False, error="json_invalido"))

    tipo = crudo.get("tipo") if isinstance(crudo, dict) else None
    if tipo != TIPO_RENTABILIDAD:
        return _respuesta(RespuestaConsulta(tipo=str(tipo), ok=False, error="tipo_no_soportado"))

    try:
        consulta = ConsultaRentabilidad.model_validate(crudo)
    except ValidationError:
        return _respuesta(RespuestaConsulta(tipo=TIPO_RENTABILIDAD, ok=False, error="filtros_invalidos"))

    f = consulta.filtros
    filtros = FiltrosRentabilidad(
        event_id=f.event_id,
        desde=f.desde,
        hasta=f.hasta,
        kind=f.kind,
        template=f.template,
        top_n=f.top_n,
    )
    try:
        resumen = calcular_rentabilidad(db, filtros)
    except KindInvalido:
        return _respuesta(RespuestaConsulta(tipo=TIPO_RENTABILIDAD, ok=False, error="kind_invalido"))
    except Exception:  # noqa: BLE001 - el consumidor nunca debe caerse por una consulta
        log.exception("error al calcular la rentabilidad")
        return _respuesta(RespuestaConsulta(tipo=TIPO_RENTABILIDAD, ok=False, error="error_interno"))

    datos = DatosRentabilidad(
        filas=[_a_salida(x) for x in resumen.filas],
        top_ganancia=[_a_salida(x) for x in resumen.top_ganancia],
        peor_ganancia=[_a_salida(x) for x in resumen.peor_ganancia],
        total_apuestas=resumen.total_apuestas,
        total_apostado=resumen.total_apostado,
        total_pagado=resumen.total_pagado,
        ganancia_neta=resumen.ganancia_neta,
    )
    return _respuesta(RespuestaConsulta(tipo=TIPO_RENTABILIDAD, ok=True, datos=datos))
