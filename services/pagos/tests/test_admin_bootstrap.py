import uuid
from types import SimpleNamespace

import pytest
from pydantic import ValidationError
from sqlalchemy import create_engine, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.core.config import Settings
from app.core.security import hash_password, verify_password
from app.db import Base
from app.models import ROLE_ADMIN, ROLE_PLAYER, User
from app.services import admin_bootstrap
from app.services.admin_bootstrap import AdminProvisioningError, provision_admin


@pytest.fixture
def db():
    engine = create_engine("sqlite+pysqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        yield session
    Base.metadata.drop_all(engine)
    engine.dispose()


def test_crea_administrador_en_base_vacia(db):
    provision_admin(db, email="admin@arqui.local", name="Admin", password="secreto")

    admin = db.scalar(select(User).where(User.email == "admin@arqui.local"))
    assert admin is not None
    assert admin.role == ROLE_ADMIN
    assert admin.balance == 0


def test_repetir_aprovisionamiento_no_duplica_ni_cambia_contrasena(db):
    provision_admin(db, email="admin@arqui.local", name="Admin", password="primera")
    admin = db.scalar(select(User).where(User.email == "admin@arqui.local"))
    original_hash = admin.password_hash
    original_id = admin.id

    provision_admin(db, email="admin@arqui.local", name="Nombre distinto", password="segunda")

    users = db.scalars(select(User).where(User.email == "admin@arqui.local")).all()
    assert len(users) == 1
    assert users[0].id == original_id
    assert users[0].name == "Admin"
    assert users[0].password_hash == original_hash
    assert verify_password("primera", users[0].password_hash)
    assert not verify_password("segunda", users[0].password_hash)


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("ADMIN_EMAIL", None),
        ("ADMIN_EMAIL", ""),
        ("ADMIN_EMAIL", "   "),
        ("ADMIN_PASSWORD", None),
        ("ADMIN_PASSWORD", ""),
        ("ADMIN_PASSWORD", "   "),
    ],
)
def test_configuracion_rechaza_credenciales_ausentes_o_vacias(monkeypatch, field, value):
    monkeypatch.setenv("ADMIN_EMAIL", "admin@arqui.local")
    monkeypatch.setenv("ADMIN_PASSWORD", "configurada")
    if value is None:
        monkeypatch.delenv(field)
    else:
        monkeypatch.setenv(field, value)

    with pytest.raises(ValidationError):
        Settings(_env_file=None)


def test_correo_de_jugador_no_se_modifica(db):
    original_hash = hash_password("cuenta-existente")
    user = User(
        email="admin@arqui.local",
        name="Usuario existente",
        password_hash=original_hash,
        role=ROLE_PLAYER,
        balance=37,
    )
    db.add(user)
    db.commit()
    original_id = user.id

    with pytest.raises(AdminProvisioningError):
        provision_admin(db, email="admin@arqui.local", name="Admin", password="nueva")

    db.expire_all()
    unchanged = db.get(User, original_id)
    assert unchanged is not None
    assert unchanged.role == ROLE_PLAYER
    assert unchanged.name == "Usuario existente"
    assert unchanged.password_hash == original_hash
    assert unchanged.balance == 37


class _ConcurrentAdminSession:
    """Simula otra instancia ganando la inserción protegida por email único."""

    def __init__(self, error_constraint="users_email_key"):
        self.admin = None
        self.pending = None
        self.rollbacks = 0
        self.error_constraint = error_constraint

    def scalar(self, _statement):
        return self.admin

    def get_bind(self):
        return SimpleNamespace(dialect=SimpleNamespace(name="postgresql"))

    def add(self, user):
        self.pending = user

    def commit(self):
        if self.admin is None:
            self.admin = User(
                id=uuid.uuid4(),
                email=self.pending.email,
                name="Admin de otra instancia",
                password_hash=hash_password("otra-clave"),
                role=ROLE_ADMIN,
            )
            original = SimpleNamespace(
                sqlstate="23505",
                diag=SimpleNamespace(
                    table_name="users",
                    constraint_name=self.error_constraint,
                ),
            )
            raise IntegrityError("insert", {}, original)
        self.admin = self.pending

    def rollback(self):
        self.rollbacks += 1
        self.pending = None


def test_conflicto_unico_de_correo_concurrente_se_acepta_si_admin_existe(monkeypatch):
    class _Inspector:
        @staticmethod
        def get_unique_constraints(_table, schema=None):
            return [{"name": "users_email_key", "column_names": ["email"]}]

    monkeypatch.setattr(admin_bootstrap, "inspect", lambda _bind: _Inspector())
    db = _ConcurrentAdminSession()

    provision_admin(db, email="admin@arqui.local", name="Admin", password="secreto")

    assert db.admin.role == ROLE_ADMIN
    assert db.admin.name == "Admin de otra instancia"
    assert db.rollbacks == 2


def test_otra_violacion_se_propaga_aunque_consulta_encuentre_admin(monkeypatch):
    class _Inspector:
        @staticmethod
        def get_unique_constraints(_table, schema=None):
            return [{"name": "users_email_key", "column_names": ["email"]}]

    monkeypatch.setattr(admin_bootstrap, "inspect", lambda _bind: _Inspector())
    db = _ConcurrentAdminSession(error_constraint="users_pkey")

    with pytest.raises(IntegrityError) as raised:
        provision_admin(
            db,
            email="admin@arqui.local",
            name="Admin",
            password=chr(116),
        )

    assert raised.value.orig.diag.constraint_name == "users_pkey"
    assert db.admin is not None
    assert db.rollbacks >= 1


def test_error_de_integridad_sin_admin_concurrente_se_propaga():
    class _UnexpectedIntegritySession(_ConcurrentAdminSession):
        def commit(self):
            raise IntegrityError("insert", {}, Exception("unrelated constraint"))

    db = _UnexpectedIntegritySession()
    with pytest.raises(IntegrityError):
        provision_admin(db, email="admin@arqui.local", name="Admin", password="secreto")
    assert db.rollbacks >= 1
