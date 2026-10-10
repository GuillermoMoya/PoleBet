"""Logica de autenticacion (HU-02).

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

from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app.core.security import hash_password, verify_password
from app.models import User


def normalizar_email(email: str) -> str:
    """Forma canonica del correo: sin espacios y en minusculas.

    IMPORTANTE para HU-01 (registro): guardar el correo con esta misma funcion,
    asi "Ana@Mail.com" y "ana@mail.com" son la misma cuenta.
    """
    return email.strip().lower()


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
