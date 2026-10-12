"""Configuracion de pruebas.

Las pruebas que tocan la BD necesitan un PostgreSQL. La forma facil:

    docker compose up -d postgres
    cd services/pagos
    DATABASE_URL=postgresql+psycopg://arqui:arqui_pass@localhost:5432/apuestas_db pytest

Si PostgreSQL no esta disponible, esas pruebas se saltan (skip) y las demas corren.
"""
import os
import uuid

import pytest

os.environ.setdefault("DATABASE_URL", "postgresql+psycopg://arqui:arqui_pass@localhost:5432/apuestas_db")
os.environ.setdefault("JWT_SECRET", "secreto-de-pruebas")
os.environ["INTERNAL_API_TOKEN"] = "token-interno-de-pruebas-para-hu07"
os.environ.setdefault("ALLOWED_ORIGINS", "http://localhost:5173")
os.environ.setdefault("DB_CONNECT_RETRIES", "1")
os.environ.setdefault("DB_CONNECT_RETRY_SECONDS", "0")
os.environ["ADMIN_EMAIL"] = f"admin-bootstrap-test-{uuid.uuid4().hex}@arqui.local"
os.environ["ADMIN_PASSWORD"] = "contrasena-de-pruebas"

from app.db import ping_db  # noqa: E402

DB_AVAILABLE = ping_db()
requires_db = pytest.mark.skipif(not DB_AVAILABLE, reason="PostgreSQL no disponible")


@pytest.fixture
def client():
    from fastapi.testclient import TestClient
    from sqlalchemy import delete

    from app.core.config import settings
    from app.db import SessionLocal
    from app.main import app
    from app.models import ROLE_ADMIN, Bet, User

    try:
        with TestClient(app) as c:  # "with" ejecuta el lifespan (crea tablas)
            yield c
    finally:
        with SessionLocal() as db:
            db.execute(
                delete(User).where(
                    User.email == settings.admin_email,
                    User.role == ROLE_ADMIN,
                )
            )
            db.commit()


@pytest.fixture
def make_user():
    """Crea usuarios de prueba y los borra al terminar."""
    from app.core.security import hash_password
    from app.db import SessionLocal
    from app.models import User

    created = []

    def _make(role="player"):
        with SessionLocal() as db:
            user = User(
                email=f"test-{uuid.uuid4().hex[:8]}@arqui.local",
                name="Prueba",
                password_hash=hash_password("clave123"),
                role=role,
            )
            db.add(user)
            db.commit()
            created.append(user.id)
            return user

    yield _make

    from sqlalchemy import delete

    from app.models import Bet, LedgerEntry

    with SessionLocal() as db:
        # Primero sus movimientos del ledger (HU-03): tienen FK al usuario.
        if created:
            db.execute(delete(LedgerEntry).where(LedgerEntry.user_id.in_(created)))
            db.execute(delete(Bet).where(Bet.user_id.in_(created)))
        for uid in created:
            obj = db.get(User, uid)
            if obj:
                db.delete(obj)
        db.commit()
