"""Herramienta de linea de comandos para DESARROLLO.

Crea usuarios de prueba para poder probar el login (HU-02) y las historias que
dependen de el (HU-03, HU-06...) mientras no existan el registro (HU-01) ni la
creacion automatica del admin (HU-12). No reemplaza a ninguna de las dos.

Uso (con el sistema levantado):

    docker compose exec pagos python -m app.cli crear-usuario \\
        --email ana@arqui.local --nombre "Ana Perez" --password clave123

    # administrador
    docker compose exec pagos python -m app.cli crear-usuario \\
        --email admin@arqui.local --nombre Admin --password admin1234 --admin
"""
import argparse
import sys

from sqlalchemy import func, select

from app.core.security import hash_password
from app.db import SessionLocal, init_db, wait_for_db
from app.models import ROLE_ADMIN, ROLE_PLAYER, User
from app.services.auth import normalizar_email


def crear_usuario(email: str, nombre: str, password: str, admin: bool) -> int:
    correo = normalizar_email(email)
    with SessionLocal() as db:
        # Mismo criterio que el login: el correo no distingue mayusculas.
        if db.scalar(select(User).where(func.lower(User.email) == correo)):
            print(f"Ya existe un usuario con el correo {correo}. No se creo nada.")
            return 1
        user = User(
            email=correo,
            name=nombre.strip(),
            password_hash=hash_password(password),  # nunca en texto plano (RNF-06)
            role=ROLE_ADMIN if admin else ROLE_PLAYER,
        )
        db.add(user)
        db.commit()
        print(f"Usuario creado: {user.email} (rol {user.role}, id {user.id})")
        return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m app.cli", description="Herramientas de desarrollo de Pagos.")
    sub = parser.add_subparsers(dest="comando", required=True)

    crear = sub.add_parser("crear-usuario", help="Crea un usuario de prueba con su contraseña hasheada")
    crear.add_argument("--email", required=True)
    crear.add_argument("--nombre", required=True)
    crear.add_argument("--password", required=True)
    crear.add_argument("--admin", action="store_true", help="Crea el usuario con rol admin")

    args = parser.parse_args(argv)
    wait_for_db()
    init_db()  # por si se corre antes de que el servicio haya creado las tablas
    if args.comando == "crear-usuario":
        return crear_usuario(args.email, args.nombre, args.password, args.admin)
    return 2


if __name__ == "__main__":
    sys.exit(main())
