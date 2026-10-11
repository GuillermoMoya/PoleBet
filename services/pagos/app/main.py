"""Servicio Pagos y usuarios - Prototipo 1 (ArquiSoft).

Punto de entrada de FastAPI. Aqui solo se arma la aplicacion: configuracion,
CORS, conexion a PostgreSQL y registro de routers. Las HU de negocio
(registro, login, compra de tokens, debito de apuesta, liquidacion) se agregan
como routers nuevos en app/routers/ (ver app/routers/__init__.py).

Estructura:
    app/
    ├── main.py        # crea la app (este archivo)
    ├── core/config.py # variables de entorno
    ├── core/security.py # hash de contrasenas y JWT (contrato con el motor)
    ├── db.py          # engine, sesiones, espera e inicializacion de la BD
    ├── deps.py        # dependencias: DbSession, CurrentUser, AdminUser
    ├── models.py      # tablas (ver documentation/modelo-datos-pagos.pdf)
    ├── schemas/       # modelos Pydantic de entrada/salida
    ├── routers/       # endpoints REST, uno por tema
    └── services/      # logica de negocio
"""
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.core.config import settings
from app.db import SessionLocal, init_db, wait_for_db
from app.routers import ROUTERS
from app.schemas.common import MessageOut
from app.services.admin_bootstrap import provision_admin

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
logger = logging.getLogger("pagos")


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Al arrancar: esperar a PostgreSQL y crear las tablas que falten.
    wait_for_db()
    init_db()
    with SessionLocal() as db:
        provision_admin(
            db,
            email=settings.admin_email,
            name=settings.admin_name,
            password=settings.admin_password,
        )
    logger.info("Pagos listo. CORS permitido para: %s", settings.allowed_origins)
    yield


def create_app() -> FastAPI:
    app = FastAPI(title=settings.app_name, version=settings.app_version, lifespan=lifespan)

    # El front corre en otro origen y envia la cookie de sesion, por eso los
    # origenes deben ser explicitos (con credentials no se permite "*").
    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.allowed_origins,
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    for router in ROUTERS:
        app.include_router(router)

    @app.get("/", response_model=MessageOut, tags=["health"])
    def root() -> MessageOut:
        return MessageOut(service="pagos", message="Pagos y usuarios - Prototipo 1")

    return app


app = create_app()
