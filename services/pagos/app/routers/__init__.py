"""Routers de la API.

Para agregar una HU nueva:
  1. Crear app/routers/<tema>.py con `router = APIRouter(prefix="/<tema>", tags=["<tema>"])`.
  2. Registrarlo en la lista ROUTERS de abajo.
"""
from app.routers import auth, billetera, health, internal

ROUTERS = [
    health.router,
    auth.router,  # HU-02: /auth/login, /auth/logout, /auth/me
    billetera.router,  # HU-03: /billetera, /billetera/compras
    internal.router,  # HU-06/HU-07: débito y liquidación de apuestas desde Motor
]