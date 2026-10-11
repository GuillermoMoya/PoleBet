"""Logica de autenticacion (HU-01, HU-02).

El documento del prototipo pide dejar el login "detras de una interfaz de
autenticacion para migrar despues a OAuth sin tocar el resto del sistema".
Eso es `ProveedorIdentidad`: el router solo conoce esa interfaz. Hoy la
implementa `ProveedorLocal` (correo + contraseña contra PostgreSQL); mañana
puede haber un `ProveedorOAuth` y basta con cambiar `get_proveedor_identidad`.

Lo que NO cambia con OAuth: despues de identificar al usuario, Pagos sigue
emitiendo su propio JWT (core/security.py) y el motor lo sigue validando igual.
"""
from functools import lru_cache
from typing import Protocol

from fastapi import HTTPException, status
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.core.security import hash_password, verify_password
from app.models import ROLE_PLAYER, User
from app.schemas.auth import RegisterRequest


# ---------------------------------------------------------------------------
# Utilidades compartidas
# ---------------------------------------------------------------------------
def normalizar_email(email: str) -> str:
    """Forma canonica del correo: sin espacios y en minusculas.

    IMPORTANTE para HU-01 (registro): guardar el correo con esta misma funcion,
    asi "Ana@Mail.com" y "ana@mail.com" son la misma cuenta.
    """
    return email.strip().lower()


# ---------------------------------------------------------------------------
# HU-02: login (proveedor de identidad)
# ---------------------------------------------------------------------------
class ProveedorIdentidad(Protocol):
    """Interfaz de autenticacion: dice QUIEN es el usuario, nada mas."""

    def autenticar(self, db: Session, email: str, password: str) -> User | None:
        """Devuelve el usuario si las credenciales son validas, si no None."""
        ...


@lru_cache
def _hash_senuelo() -> str:
    # Hash de una contraseña cualquiera. Se calcula una sola vez, la primera
    # vez que alguien intenta entrar con un correo que no existe.
    return hash_password("contraseña-señuelo-que-nadie-usa")


class ProveedorLocal:
    """Login propio de la app: correo + contraseña con hash bcrypt (RNF-06)."""

    def autenticar(self, db: Session, email: str, password: str) -> User | None:
        correo = normalizar_email(email)
        # func.lower protege tambien a las cuentas creadas antes de normalizar.
        user = db.scalar(select(User).where(func.lower(User.email) == correo))

        if user is None:
            # Se compara igual contra un hash señuelo para que responder
            # "no existe ese correo" tarde lo mismo que "clave incorrecta".
            # Sin esto, midiendo el tiempo de respuesta se podria averiguar
            # que correos estan registrados.
            verify_password(password, _hash_senuelo())
            return None

        if not verify_password(password, user.password_hash):
            return None
        return user


def get_proveedor_identidad() -> ProveedorIdentidad:
    """Dependencia de FastAPI. Para migrar a OAuth se cambia solo esta linea.

    En las pruebas se puede reemplazar con app.dependency_overrides.
    """
    return ProveedorLocal()


# ---------------------------------------------------------------------------
# HU-01: registro
# ---------------------------------------------------------------------------
def registrar_usuario(db: Session, data: RegisterRequest) -> User:
    """Crea un usuario nuevo con rol player y saldo 0.

    Cumple HU-01:
      - Correo unico case-insensitive: "Ana@x.com" y "ana@x.com" son la misma
        cuenta. Se normaliza antes de buscar y antes de guardar.
      - Contrasena hasheada con bcrypt (RNF-06), nunca en texto plano.
      - Rol por defecto "player".
      - Saldo inicial 0.
      - Si dos peticiones llegan con el mismo correo a la vez, la segunda
        recibe 409 en lugar de un 500.
    """
    email = normalizar_email(data.email)

    # 1. Chequeo rapido para dar 409 en el caso normal. NO es la unica defensa:
    #    dos peticiones simultaneas pueden pasar este select a la vez y solo
    #    una ganara el commit (ver el try/except de abajo).
    existente = db.scalar(select(User).where(User.email == email))
    if existente is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="Este correo ya está registrado",
        )

    # 2. Hash de la contrasena (RNF-06).
    password_hash = hash_password(data.password)

    # 3. Crear el usuario con rol player y saldo 0.
    user = User(
        email=email,
        name=data.name,
        password_hash=password_hash,
        role=ROLE_PLAYER,
        balance=0,
    )
    db.add(user)
    try:
        db.commit()
    except IntegrityError:
        # Otra peticion gano la carrera por el mismo correo: el UNIQUE de
        # Postgres salta y aqui lo convertimos en el mismo 409 que arriba.
        db.rollback()
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="Este correo ya está registrado",
        )
    db.refresh(user)
    return user