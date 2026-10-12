"""Contratos REST internos usados por Motor."""
import uuid
from decimal import Decimal
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class InternalRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")


class BetDebitIn(InternalRequest):
    bet_id: uuid.UUID
    user_id: uuid.UUID
    market_id: str = Field(min_length=1, max_length=64)
    option: str = Field(min_length=1, max_length=100)
    amount: int = Field(gt=0)
    idempotency_key: str = Field(min_length=1, max_length=255)


class BetDebitOut(BaseModel):
    bet_id: uuid.UUID
    status: Literal["pending", "won", "lost"]
    saldo: int
    nueva: bool


class BetSettlementIn(InternalRequest):
    bet_id: uuid.UUID
    result: Literal["won", "lost"]
    points: Decimal = Field(ge=0, max_digits=12, decimal_places=1)
    idempotency_key: str = Field(min_length=1, max_length=255)


class BetSettlementOut(BaseModel):
    bet_id: uuid.UUID
    status: Literal["won", "lost"]
    points_awarded: float
    points_total: float
    nueva: bool
