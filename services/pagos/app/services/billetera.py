"""Logica de la billetera: compra de tokens simulada (HU-03, RF-03).

Criterios que cubre:
  - El pago se aprueba sin pasarela real y el monto en COP se convierte con
    la tasa fija COP_PER_TOKEN.
  - Montos <= 0 (o que no dan tokens enteros) se rechazan.
  - Idempotencia (RNF-02): la misma Idempotency-Key acredita una sola vez,
    aunque llegue dos veces a la vez.
  - Transaccional (RNF-01): el movimiento del ledger y el saldo se guardan
    juntos o no se guarda nada.
  - Ledger (RNF-10): cada compra queda con usuario, tipo, tokens, fecha y estado.

El saldo nunca se lee, se suma en Python y se escribe: se hace
`balance = balance + x` en un solo UPDATE, asi dos compras simultaneas del
mismo usuario no se pisan.
"""
import uuid
from dataclasses import dataclass

from sqlalchemy import select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.core.config import settings
from app.models import LEDGER_COMPLETED, LEDGER_PURCHASE, LedgerEntry, User

# Tope por compra, para que nadie se acredite cantidades absurdas en la demo.
MONTO_MAXIMO_COP = 10_000_000


class MontoInvalido(ValueError):
    """El monto no se puede comprar. El mensaje se muestra tal cual al jugador."""


class ClaveReutilizada(Exception):
    """La Idempotency-Key ya se uso para una compra distinta."""


@dataclass
class ResultadoCompra:
    movimiento: LedgerEntry
    saldo: int  # saldo del usuario despues de la compra
    nueva: bool  # False si era un reenvio de una compra ya hecha


def tasa() -> int:
    """COP por token (COP_PER_TOKEN)."""
    return int(settings.cop_per_token)


def tokens_por_monto(monto_cop: int) -> int:
    """Convierte el monto a tokens o lanza MontoInvalido."""
    cop_por_token = tasa()
    if monto_cop <= 0:
        raise MontoInvalido("El monto debe ser mayor que 0")
    if monto_cop > MONTO_MAXIMO_COP:
        raise MontoInvalido(f"El monto máximo por compra es ${MONTO_MAXIMO_COP:,} COP".replace(",", "."))
    if monto_cop % cop_por_token != 0:
        # Se rechaza en vez de redondear: no se cobra plata que no se
        # convierte en tokens.
        raise MontoInvalido(f"El monto debe ser múltiplo de ${cop_por_token:,} COP".replace(",", "."))
    return monto_cop // cop_por_token


def clave_ledger(user_id: uuid.UUID, clave: str) -> str:
    """Clave que se guarda en ledger_entries.idempotency_key.

    La columna es unica en toda la tabla y la comparten las compras y los
    debitos del motor (HU-06). El prefijo evita que dos usuarios, o una compra
    y una apuesta, choquen por usar la misma clave.
    """
    return f"compra:{user_id}:{clave}"


def saldo_de(db: Session, user_id: uuid.UUID) -> int:
    return db.scalar(select(User.balance).where(User.id == user_id))


def _reenvio(db: Session, previo: LedgerEntry, user_id: uuid.UUID, tokens: int) -> ResultadoCompra:
    """Respuesta a una clave ya usada: la compra original, sin acreditar de nuevo."""
    if previo.type != LEDGER_PURCHASE or previo.tokens_delta != tokens:
        # Misma clave con otro monto: no es un reenvio, es otra compra.
        raise ClaveReutilizada()
    return ResultadoCompra(movimiento=previo, saldo=saldo_de(db, user_id), nueva=False)


def comprar_tokens(db: Session, user_id: uuid.UUID, monto_cop: int, clave: str) -> ResultadoCompra:
    """Acredita los tokens de la compra, una sola vez por clave."""
    tokens = tokens_por_monto(monto_cop)
    clave_guardada = clave_ledger(user_id, clave)

    # 1. Reenvio normal (doble clic, reintento del navegador): ya existe.
    previo = db.scalar(select(LedgerEntry).where(LedgerEntry.idempotency_key == clave_guardada))
    if previo is not None:
        return _reenvio(db, previo, user_id, tokens)

    # 2. Compra nueva. Movimiento y saldo en la misma transaccion.
    movimiento = LedgerEntry(
        user_id=user_id,
        type=LEDGER_PURCHASE,
        tokens_delta=tokens,
        status=LEDGER_COMPLETED,  # el pago simulado siempre se aprueba
        idempotency_key=clave_guardada,
    )
    db.add(movimiento)
    try:
        # Si otra peticion con la misma clave entro al mismo tiempo, Postgres
        # hace esperar a esta hasta que la otra confirme y luego rechaza el
        # INSERT por la restriccion unica.
        db.flush()
    except IntegrityError:
        db.rollback()
        previo = db.scalar(select(LedgerEntry).where(LedgerEntry.idempotency_key == clave_guardada))
        if previo is None:
            raise
        return _reenvio(db, previo, user_id, tokens)

    saldo = db.execute(
        update(User).where(User.id == user_id).values(balance=User.balance + tokens).returning(User.balance)
    ).scalar_one()
    db.commit()
    db.refresh(movimiento)  # trae created_at, que lo pone Postgres
    return ResultadoCompra(movimiento=movimiento, saldo=saldo, nueva=True)
