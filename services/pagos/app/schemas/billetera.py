"""Esquemas de entrada y salida de la billetera (HU-03).

Los nombres de los campos van en español porque asi los espera el front
(services/front/src/paginas/Billetera.tsx).
"""
from datetime import datetime

from pydantic import BaseModel, Field

from app.services.billetera import ResultadoCompra


class BilleteraOut(BaseModel):
    """Respuesta de GET /billetera."""

    saldo: int  # tokens disponibles
    cop_por_token: int  # tasa fija (COP_PER_TOKEN); el front la usa para "Recibes X PBT"


class CompraIn(BaseModel):
    """Cuerpo de POST /billetera/compras.

    Solo se valida el tipo. Las reglas del monto (mayor que 0, multiplo de la
    tasa, tope) las aplica el servicio, para responder con un mensaje en
    español que el front pueda mostrar tal cual.
    """

    monto_cop: int = Field(examples=[50000])


class CompraOut(BaseModel):
    """Respuesta de POST /billetera/compras (201 la primera vez, 200 en un reenvio)."""

    compra_id: int  # id del movimiento en el ledger
    tokens_acreditados: int
    saldo: int  # saldo despues de la compra
    creada_en: datetime

    @classmethod
    def desde_resultado(cls, r: ResultadoCompra) -> "CompraOut":
        return cls(
            compra_id=r.movimiento.id,
            tokens_acreditados=r.movimiento.tokens_delta,
            saldo=r.saldo,
            creada_en=r.movimiento.created_at,
        )
