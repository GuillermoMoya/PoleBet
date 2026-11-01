"""Configuracion del servicio Pagos, leida de variables de entorno.

En Docker los valores llegan desde docker-compose.yml (que a su vez lee el
.env de la raiz). Fuera de Docker se puede crear un .env dentro de
services/pagos/ con las mismas variables.
"""
from functools import lru_cache

from pydantic import field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # ---- Servicio ----
    app_name: str = "Pagos"
    app_version: str = "0.4.0"

    # ---- PostgreSQL Pagos ----
    database_url: str = "postgresql+psycopg://arqui:arqui_pass@localhost:5432/pagos_db"
    db_connect_retries: int = 10
    db_connect_retry_seconds: float = 2.0

    # ---- Token de servicio para /internal/* (RNF-17) ----
    internal_api_token: str

    # ---- Economia del token (RF-05) ----
    cop_per_token: float = 1000.0

    # ---- Torneo multijugador (HU2-06), valores tentativos ----
    # buy_in, fichas y comision son los valores por defecto al crear un evento
    # (quedan guardados en events). min_players y min_wager_pct no estan en el
    # modelo de datos y se leen siempre de aqui.
    event_buy_in: int = 100  # PoleCoins
    event_initial_chips: int = 1000  # fichas de juego por inscrito (no son PoleCoins)
    event_commission_pct: int = 10  # comision de la casa sobre el botin
    event_min_players: int = 4  # con menos, el torneo se cancela y se devuelve el buy_in
    event_min_wager_pct: int = 50  # % de fichas que hay que apostar para cobrar (HU2-12)

    @field_validator("internal_api_token")
    @classmethod
    def _internal_api_token_required(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError("INTERNAL_API_TOKEN no puede estar vacío")
        if len(value) < 32:
            raise ValueError("INTERNAL_API_TOKEN debe tener al menos 32 caracteres")
        return value


@lru_cache
def get_settings() -> Settings:
    return Settings()


settings = get_settings()
