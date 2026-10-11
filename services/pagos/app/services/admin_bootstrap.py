"""Aprovisionamiento idempotente del administrador inicial."""
from sqlalchemy import inspect, select
from sqlalchemy.exc import IntegrityError, SQLAlchemyError
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models import ROLE_ADMIN, User


class AdminProvisioningError(RuntimeError):
    """La identidad configurada para el administrador no se puede aprovisionar."""


def _is_email_unique_violation(db: Session, exc: IntegrityError) -> bool:
    original = exc.orig
    diagnostic = getattr(original, "diag", None)
    constraint_name = getattr(diagnostic, "constraint_name", None)
    if (
        db.get_bind().dialect.name != "postgresql"
        or getattr(original, "sqlstate", None) != "23505"
        or getattr(diagnostic, "table_name", None) != User.__tablename__
        or not constraint_name
    ):
        return False

    try:
        constraints = inspect(db.get_bind()).get_unique_constraints(
            User.__tablename__, schema=User.__table__.schema
        )
    except SQLAlchemyError:
        return False
    return any(
        constraint.get("name") == constraint_name
        and constraint.get("column_names") == ["email"]
        for constraint in constraints
    )


def provision_admin(db: Session, *, email: str, name: str, password: str) -> None:
    """Crea el administrador si falta; nunca altera una cuenta existente."""
    try:
        existing = db.scalar(select(User).where(User.email == email))
        if existing is not None:
            if existing.role == ROLE_ADMIN:
                db.rollback()
                return
            raise AdminProvisioningError(
                "El correo configurado para ADMIN_EMAIL ya pertenece a un usuario incompatible"
            )

        db.add(
            User(
                email=email,
                name=name,
                password_hash=hash_password(password),
                role=ROLE_ADMIN,
            )
        )
        try:
            db.commit()
        except IntegrityError as exc:
            db.rollback()
            if not _is_email_unique_violation(db, exc):
                raise
            existing = db.scalar(select(User).where(User.email == email))
            if existing is not None and existing.role == ROLE_ADMIN:
                db.rollback()
                return
            if existing is not None:
                raise AdminProvisioningError(
                    "El correo configurado para ADMIN_EMAIL ya pertenece a un usuario incompatible"
                ) from exc
            raise
    except AdminProvisioningError:
        db.rollback()
        raise
    except SQLAlchemyError:
        db.rollback()
        raise
