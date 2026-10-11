"""Configuracion del servicio Pagos y usuarios, leida de variables de entorno.

En Docker los valores llegan desde docker-compose.yml (que a su vez lee el
.env de la raiz). Fuera de Docker se puede crear un .env dentro de
services/pagos/ con las mismas variables.
"""
from functools import lru_cache
from typing import Annotated

from pydantic import field_validator
from pydantic_settings import BaseSettings, NoDecode, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # ---- Servicio ----
    app_name: str = "Pagos y usuarios"
    app_version: str = "0.2.0"

    # ---- PostgreSQL ----
    # Mismo nombre de BD que .env.example para que funcione igual fuera de Docker.
    database_url: str = "postgresql+psycopg://arqui:arqui_pass@localhost:5432/apuestas_db"
    db_connect_retries: int = 10
    db_connect_retry_seconds: float = 2.0

    # ---- CORS ----
    # Lista separada por comas, p. ej. "http://localhost:5173,http://127.0.0.1:5173".
    # No puede ser "*" porque el front envia la cookie de sesion (credentials).
    allowed_origins: Annotated[list[str], NoDecode] = [
        "http://localhost:5173",
        "http://127.0.0.1:5173",
    ]

    # ---- JWT (contrato compartido con el motor, ver services/motor/internal/auth/contrato.go) ----
    jwt_secret: str = "dev-secret"
    jwt_algorithm: str = "HS256"
    jwt_expire_minutes: int = 120
    session_cookie_name: str = "session"
    # En produccion con HTTPS debe ser True. En la demo local (http) va en False.
    session_cookie_secure: bool = False

    # ---- Economia del token (RF-03) ----
    cop_per_token: float = 1000.0

    # ---- Admin inicial (RF-13 / HU-12) ----
    admin_email: str
    admin_password: str
    admin_name: str = "Administrador"

    @field_validator("admin_email")
    @classmethod
    def _admin_email_required(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError("ADMIN_EMAIL no puede estar vacío")
        return value

    @field_validator("admin_password")
    @classmethod
    def _admin_password_required(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("ADMIN_PASSWORD no puede estar vacío")
        return value

    @field_validator("allowed_origins", mode="before")
    @classmethod
    def _split_origins(cls, value):
        if isinstance(value, str):
            return [o.strip() for o in value.split(",") if o.strip()]
        return value


@lru_cache
def get_settings() -> Settings:
    return Settings()


settings = get_settings()
