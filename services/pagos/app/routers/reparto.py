"""HU2-12: ruta interna de cierre del evento y reparto del botin.

  POST /internal/events/{event_id}/close   Motor: fichas finales -> premios

Vive en su propio archivo (y no en routers/eventos.py de HU2-06) para que cada
historia toque solo sus archivos. Comparte el prefijo /internal/events.
Exige Idempotency-Key = event-close:{event_id} y el token de servicio.
"""
import uuid
from typing import Annotated

from fastapi import APIRouter, Header, HTTPException, Response, status

from app.deps import DbSession, ServicioInterno
from app.schemas.reparto import EventCloseIn, EventCloseOut, ResultadoJugadorOut
from app.services.reparto import (
    BilleteraNoExiste,
    CierreDistinto,
    EventoNoExiste,
    EventoNoLargado,
    FichasFinales,
    InscritosNoCoinciden,
    cerrar_evento,
    clave_cierre,
)

router = APIRouter(prefix="/internal/events", tags=["reparto"], dependencies=[ServicioInterno])


@router.post(
    "/{event_id}/close",
    response_model=EventCloseOut,
    status_code=status.HTTP_201_CREATED,
    responses={
        200: {"description": "Ya estaba cerrado: se devuelve el reparto guardado, sin pagar de nuevo"},
        400: {"description": "Idempotency-Key inválida"},
        404: {"description": "Evento o billetera de un ganador inexistente"},
        409: {"description": "El evento no ha largado, ya se repartió con otras fichas, o los jugadores no coinciden con los inscritos"},
    },
)
def cerrar(
    event_id: uuid.UUID,
    datos: EventCloseIn,
    db: DbSession,
    response: Response,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> EventCloseOut:
    if idempotency_key != clave_cierre(event_id):
        raise HTTPException(status.HTTP_400_BAD_REQUEST, "Idempotency-Key inválida")
    fichas = [FichasFinales(p.user_id, p.chips_final, p.chips_wagered) for p in datos.players]
    try:
        r = cerrar_evento(db, event_id, fichas)
    except EventoNoExiste:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "El evento no existe") from None
    except BilleteraNoExiste:
        raise HTTPException(status.HTTP_404_NOT_FOUND, "Un ganador no tiene billetera") from None
    except EventoNoLargado:
        raise HTTPException(status.HTTP_409_CONFLICT, "El evento no tiene un torneo en curso que cerrar") from None
    except CierreDistinto:
        raise HTTPException(status.HTTP_409_CONFLICT, "El evento ya se repartió con otras fichas finales") from None
    except InscritosNoCoinciden:
        raise HTTPException(status.HTTP_409_CONFLICT, "Los jugadores enviados no coinciden con los inscritos") from None

    if not r.nueva:
        response.status_code = status.HTTP_200_OK
    rep = r.reparto
    return EventCloseOut(
        event_id=r.evento.id,
        status=r.evento.status,
        tournament_status=r.evento.tournament_status,
        pot=r.evento.pot,
        commission=rep.commission,
        prizes_total=rep.prizes_total,
        residue=rep.residue,
        players=[
            ResultadoJugadorOut(
                user_id=j.user_id,
                position_final=j.position_final,
                chips_final=j.chips_final,
                chips_wagered=j.chips_wagered,
                eligible=j.eligible,
                prize=j.prize,
            )
            for j in rep.jugadores
        ],
        nueva=r.nueva,
    )
