"""Compra de tokens simulada (HU2-04, RF-05). La llama el Orquestador.

    POST /internal/purchases   {user_id, monto_cop} + Idempotency-Key: purchase:{user_id}:{clave}
"""
from typing import Annotated

from fastapi import APIRouter, Header, HTTPException, Response, status

from app.deps import DbSession, ServicioInterno
from app.schemas.purchases import CompraIn, CompraOut
from app.services.billetera import BilleteraNoExiste, ClaveReutilizada, MontoInvalido, comprar_tokens

router = APIRouter(prefix="/internal/purchases", tags=["purchases"], dependencies=[ServicioInterno])

# Largo de la columna ledger_entries.idempotency_key.
LARGO_MAXIMO_CLAVE = 255


@router.post(
    "",
    response_model=CompraOut,
    status_code=status.HTTP_201_CREATED,
    responses={
        200: {"description": "Reenvío de una compra ya hecha: no se acredita de nuevo"},
        400: {"description": "Idempotency-Key ausente o distinta de purchase:{user_id}:{clave}"},
        401: {"description": "Token de servicio ausente o inválido"},
        404: {"description": "El usuario no tiene billetera"},
        409: {"description": "La Idempotency-Key ya se usó con otro monto"},
        422: {"description": "Monto inválido (<= 0, no múltiplo de la tasa o sobre el tope)"},
    },
)
def comprar(
    datos: CompraIn,
    db: DbSession,
    response: Response,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> CompraOut:
    """Compra tokens con un pago simulado (siempre aprobado).

    - 201: compra nueva; devuelve los tokens acreditados y el saldo nuevo.
    - 200: la misma Idempotency-Key ya se había procesado (reintento);
      devuelve esa compra y el saldo actual, sin acreditar de nuevo.
    """
    prefijo = f"purchase:{datos.user_id}:"
    clave = idempotency_key or ""
    if not clave.startswith(prefijo) or len(clave) == len(prefijo) or len(clave) > LARGO_MAXIMO_CLAVE:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="Idempotency-Key inválida")

    try:
        resultado = comprar_tokens(db, datos.user_id, datos.monto_cop, clave)
    except MontoInvalido as error:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail=str(error)) from None
    except ClaveReutilizada:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="Esta solicitud ya se usó para otra compra") from None
    except BilleteraNoExiste:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Billetera inexistente") from None

    if not resultado.nueva:
        response.status_code = status.HTTP_200_OK
    return CompraOut.desde_resultado(resultado)
