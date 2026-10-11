"""Esquemas de entrada y salida de la autenticacion (HU-02).

Los nombres de los campos de salida van en español porque asi los espera el
front (services/front/src/auth/AuthContext.tsx, interfaz `Usuario`).
"""
import uuid
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

from app.models import User


class LoginIn(BaseModel):
    """Cuerpo de POST /auth/login."""

    # El formato del correo no se valida aqui a proposito: si alguien escribe
    # mal su correo, la respuesta debe ser la misma de cualquier credencial
    # invalida ("Correo o contraseña incorrectos"). Validar el formato es
    # trabajo del registro (HU-01).
    email: str = Field(min_length=3, max_length=255, examples=["jugador@arqui.local"])
    password: str = Field(min_length=1, max_length=128, examples=["clave123"])


class UsuarioOut(BaseModel):
    """Lo que el front sabe del usuario con sesion.

    Solo identidad. El saldo y los puntos NO van aqui: cambian a cada rato y
    tienen sus propios endpoints/mensajes (HU-03 y HU-08).
    """

    model_config = ConfigDict(from_attributes=True)

    id: uuid.UUID
    nombre: str
    email: str
    rol: Literal["player", "admin"]

    @classmethod
    def desde_modelo(cls, user: User) -> "UsuarioOut":
        """Traduce el modelo ORM (nombres en ingles) al contrato del front."""
        return cls(id=user.id, nombre=user.name, email=user.email, rol=user.role)
