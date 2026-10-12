"""Endpoints REST autenticados para la integración Motor → Pagos."""
import uuid
from typing import Annotated

from fastapi import APIRouter, Header, HTTPException, Response, status

from app.core.internal_auth import token_servicio_valido
from app.deps import DbSession
from app.schemas.internal import (
    BetDebitIn,
    BetDebitOut,
    BetSettlementIn,
    BetSettlementOut,
)
from app.services.liquidacion import (
    ApuestaNoExiste,
    BetDebitData,
    ClaveReutilizada,
    PuntosIncorrectos,
    SaldoInsuficiente,
    UsuarioNoExiste,
    debitar_apuesta,
    liquidar_apuesta,
)

router = APIRouter(prefix="/internal", tags=["internal"])


def _verificar_servicio(token: str | None) -> None:
    if not token_servicio_valido(token):
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="Servicio no autorizado")


def _validar_clave(header: str | None, cuerpo: str, prefijo: str, bet_id: uuid.UUID) -> None:
    esperada = f"{prefijo}:{bet_id}"
    if header != cuerpo or cuerpo != esperada:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="Idempotency-Key inválida")


@router.post(
    "/debit",
    response_model=BetDebitOut,
    status_code=status.HTTP_201_CREATED,
    responses={
        200: {"description": "Reenvío idempotente del débito"},
        400: {"description": "Clave de idempotencia inválida"},
        401: {"description": "Token de servicio ausente o inválido"},
        404: {"description": "Usuario inexistente"},
        409: {"description": "ID o clave reutilizados con otro contenido"},
        422: {"description": "Saldo insuficiente o cuerpo inválido"},
    },
)
def debitar(
    datos: BetDebitIn,
    db: DbSession,
    response: Response,
    service_token: Annotated[str | None, Header(alias="X-Internal-Service-Token")] = None,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> BetDebitOut:
    _verificar_servicio(service_token)
    _validar_clave(idempotency_key, datos.idempotency_key, "bet-debit", datos.bet_id)
    try:
        resultado = debitar_apuesta(
            db,
            BetDebitData(
                bet_id=datos.bet_id,
                user_id=datos.user_id,
                market_id=datos.market_id,
                option=datos.option,
                amount=datos.amount,
                idempotency_key=datos.idempotency_key,
            ),
        )
    except UsuarioNoExiste:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Usuario inexistente") from None
    except SaldoInsuficiente:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail="Saldo insuficiente") from None
    except ClaveReutilizada:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="Clave reutilizada") from None

    if not resultado.nueva:
        response.status_code = status.HTTP_200_OK
    return BetDebitOut(
        bet_id=resultado.apuesta.id,
        status=resultado.apuesta.status,
        saldo=resultado.saldo,
        nueva=resultado.nueva,
    )


@router.post(
    "/settlements",
    response_model=BetSettlementOut,
    status_code=status.HTTP_201_CREATED,
    responses={
        200: {"description": "Reenvío idempotente de la liquidación"},
        400: {"description": "Clave de idempotencia inválida"},
        401: {"description": "Token de servicio ausente o inválido"},
        404: {"description": "Apuesta inexistente"},
        409: {"description": "Apuesta o clave ya usadas con otro resultado"},
        422: {"description": "Resultado o puntos inválidos"},
    },
)
def liquidar(
    datos: BetSettlementIn,
    db: DbSession,
    response: Response,
    service_token: Annotated[str | None, Header(alias="X-Internal-Service-Token")] = None,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> BetSettlementOut:
    _verificar_servicio(service_token)
    _validar_clave(idempotency_key, datos.idempotency_key, "bet-settlement", datos.bet_id)
    try:
        resultado = liquidar_apuesta(
            db,
            datos.bet_id,
            datos.result,
            datos.points,
            datos.idempotency_key,
        )
    except ApuestaNoExiste:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Apuesta inexistente") from None
    except ClaveReutilizada:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="Liquidación ya procesada con otro contenido") from None
    except PuntosIncorrectos:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail="Resultado o puntos inválidos") from None

    if not resultado.nueva:
        response.status_code = status.HTTP_200_OK
    return BetSettlementOut(
        bet_id=resultado.apuesta.id,
        status=resultado.apuesta.status,
        points_awarded=float(resultado.movimiento.points_delta),
        points_total=float(resultado.puntos_totales),
        nueva=resultado.nueva,
    )
