"""Routers de la API.

Para agregar una HU nueva:
  1. Crear app/routers/<tema>.py con `router = APIRouter(prefix="/<tema>", tags=["<tema>"])`.
  2. Registrarlo en la lista ROUTERS de abajo.
"""
from app.routers import auth, billetera, health

ROUTERS = [
    health.router,
    auth.router,  # HU-02: /auth/login, /auth/logout, /auth/me
    billetera.router,  # HU-03: /billetera, /billetera/compras
]
