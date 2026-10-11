"""Endpoints de la billetera: saldo y compra de tokens simulada (HU-03, RF-03).

    GET  /billetera           saldo del jugador y tasa COP por token
    POST /billetera/compras   compra tokens; exige el header Idempotency-Key

Solo jugadores: el administrador recibe 403 (HU-12).
"""
from typing import Annotated

from fastapi import APIRouter, Header, HTTPException, Response, status

from app.deps import DbSession, PlayerUser
from app.schemas.billetera import BilleteraOut, CompraIn, CompraOut
from app.services.billetera import ClaveReutilizada, MontoInvalido, comprar_tokens, saldo_de, tasa

router = APIRouter(prefix="/billetera", tags=["billetera"])

# Largo maximo de la Idempotency-Key (la columna admite 255 con el prefijo).
LARGO_MAXIMO_CLAVE = 128


def _sin_cache(response: Response) -> None:
    # El saldo cambia a cada rato: que ningun proxy ni el navegador lo guarde.
    response.headers["Cache-Control"] = "no-store"


@router.get("", response_model=BilleteraOut, responses={401: {"description": "No hay sesion valida"}, 403: {"description": "Solo los jugadores"}})
def ver_billetera(user: PlayerUser, db: DbSession, response: Response) -> BilleteraOut:
    """Saldo actual del jugador y la tasa fija de conversion."""
    _sin_cache(response)
    return BilleteraOut(saldo=saldo_de(db, user.id), cop_por_token=tasa())


@router.post(
    "/compras",
    response_model=CompraOut,
    status_code=status.HTTP_201_CREATED,
    responses={
        200: {"description": "Reenvio de una compra ya hecha: no se acredita de nuevo"},
        400: {"description": "Falta el header Idempotency-Key o es invalido"},
        401: {"description": "No hay sesion valida"},
        403: {"description": "Solo los jugadores"},
        409: {"description": "La Idempotency-Key ya se uso con otro monto"},
        422: {"description": "Monto invalido (<= 0, no multiplo de la tasa o sobre el tope)"},
    },
)
def comprar(
    datos: CompraIn,
    user: PlayerUser,
    db: DbSession,
    response: Response,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> CompraOut:
    """Compra tokens con un pago simulado (siempre aprobado).

    - 201: compra nueva; devuelve los tokens acreditados y el saldo nuevo.
    - 200: la misma Idempotency-Key ya se habia procesado (doble clic o
      reintento); devuelve esa compra y el saldo actual, sin acreditar de nuevo.
    """
    clave = (idempotency_key or "").strip()
    if not clave or len(clave) > LARGO_MAXIMO_CLAVE:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="Falta el header Idempotency-Key o es inválido")

    try:
        resultado = comprar_tokens(db, user.id, datos.monto_cop, clave)
    except MontoInvalido as error:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail=str(error)) from None
    except ClaveReutilizada:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT, detail="Esta solicitud ya se usó para otra compra"
        ) from None

    if not resultado.nueva:
        response.status_code = status.HTTP_200_OK
    _sin_cache(response)
    return CompraOut.desde_resultado(resultado)
