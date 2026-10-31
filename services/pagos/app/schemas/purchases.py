"""Contrato interno de la compra de tokens (ver documentation/orquestador-api.md, §3.3)."""
import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict

from app.services.billetera import ResultadoCompra


class CompraIn(BaseModel):
    """Cuerpo de POST /internal/purchases.

    Solo se valida el tipo. Las reglas del monto (mayor que 0, multiplo de la
    tasa, tope) las aplica el servicio y responden 422.
    """

    model_config = ConfigDict(extra="forbid")

    user_id: uuid.UUID
    monto_cop: int


class CompraOut(BaseModel):
    """Respuesta de POST /internal/purchases (201 la primera vez, 200 en un reenvio)."""

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
