"""Routers de la API.

Para agregar una HU nueva:
  1. Crear app/routers/<tema>.py con `router = APIRouter(prefix="/internal/<tema>", dependencies=[ServicioInterno])`.
  2. Registrarlo en la lista ROUTERS de abajo.
"""
from app.routers import eventos, health, internal, wallets

ROUTERS = [
    health.router,
    wallets.router,  # T2-03: /internal/wallets
    internal.router,  # débito y liquidación de apuestas individuales desde Motor
    eventos.router,  # HU2-06: eventos e inscripción al torneo
]
