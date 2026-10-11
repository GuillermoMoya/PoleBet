"""Pruebas de autenticacion (HU-01 registro, HU-02 login/logout/me).

Cada prueba corresponde a un criterio de aceptacion de la historia o a un
punto del contrato con el motor (services/motor/internal/auth/contrato.go).
"""
import uuid
from datetime import datetime, timedelta, timezone

import jwt
import pytest
from fastapi import APIRouter
from sqlalchemy import select

from app.core.config import settings
from app.core.security import decode_access_token, verify_password
from app.deps import PlayerUser
from app.models import ROLE_PLAYER, User
from tests.conftest import requires_db

CLAVE = "clave123"  # la que usa el fixture make_user
MENSAJE = "Correo o contraseña incorrectos"


def _login(client, email, password=CLAVE):
    return client.post("/auth/login", json={"email": email, "password": password})


def _payload(**overrides):
    """Datos para POST /auth/register. El correo es unico por defecto."""
    base = {
        "name": "Ivan Test",
        "email": f"ivan-{uuid.uuid4().hex[:8]}@example.com",
        "password": "clave-segura-123",
    }
    base.update(overrides)
    return base


# ===========================================================================
# HU-02: login, logout, /auth/me
# ===========================================================================

# ---------------------------------------------------------------------------
# Criterio: credenciales correctas -> JWT en cookie HttpOnly
# ---------------------------------------------------------------------------
@requires_db
def test_login_correcto_pone_cookie_httponly_con_el_jwt(client, make_user):
    user = make_user("player")
    r = _login(client, user.email)

    assert r.status_code == 200
    assert r.json() == {"id": str(user.id), "nombre": user.name, "email": user.email, "rol": "player"}
    assert r.headers["cache-control"] == "no-store"

    set_cookie = r.headers["set-cookie"]
    assert set_cookie.startswith(f"{settings.session_cookie_name}=")
    assert "HttpOnly" in set_cookie
    assert "SameSite=lax" in set_cookie
    assert "Path=/" in set_cookie
    assert f"Max-Age={settings.jwt_expire_minutes * 60}" in set_cookie

    # El token no viaja en el cuerpo: solo en la cookie.
    token = client.cookies.get(settings.session_cookie_name)
    assert token and token not in r.text


@requires_db
def test_el_jwt_cumple_el_contrato_del_motor(client, make_user):
    admin = make_user("admin")
    _login(client, admin.email)
    token = client.cookies.get(settings.session_cookie_name)

    assert jwt.get_unverified_header(token)["alg"] == "HS256"
    claims = jwt.decode(token, settings.jwt_secret, algorithms=["HS256"])
    assert claims["sub"] == str(admin.id)
    assert claims["rol"] == "admin"  # HU-11 lo usa para abrir mercados
    assert claims["exp"] > claims["iat"]
    assert decode_access_token(token) == (admin.id, "admin")


@requires_db
def test_el_correo_no_distingue_mayusculas_ni_espacios(client, make_user):
    user = make_user("player")
    r = _login(client, f"  {user.email.upper()}  ")
    assert r.status_code == 200
    assert r.json()["id"] == str(user.id)


# ---------------------------------------------------------------------------
# Criterio: credenciales incorrectas -> mensaje generico, sin decir cual fallo
# ---------------------------------------------------------------------------
@requires_db
def test_clave_incorrecta_y_correo_inexistente_dan_el_mismo_error(client, make_user):
    user = make_user("player")
    mala_clave = _login(client, user.email, "otra-clave")
    sin_cuenta = _login(client, "nadie@arqui.local")

    for r in (mala_clave, sin_cuenta):
        assert r.status_code == 401
        assert r.json() == {"detail": MENSAJE}
        assert "set-cookie" not in r.headers
    assert client.cookies.get(settings.session_cookie_name) is None


@requires_db
def test_cuerpo_incompleto_responde_422(client):
    assert client.post("/auth/login", json={"email": "a@b.co"}).status_code == 422
    assert client.post("/auth/login", json={"email": "a@b.co", "password": ""}).status_code == 422


# ---------------------------------------------------------------------------
# Criterio: JWT vencido o inexistente -> 401
# ---------------------------------------------------------------------------
@requires_db
def test_me_sin_sesion_responde_401(client):
    r = client.get("/auth/me")
    assert r.status_code == 401


@requires_db
def test_me_con_sesion_devuelve_el_usuario_sin_datos_sensibles(client, make_user):
    user = make_user("player")
    _login(client, user.email)

    r = client.get("/auth/me")
    assert r.status_code == 200
    assert r.json()["id"] == str(user.id)
    assert "password" not in r.text and "hash" not in r.text


@requires_db
def test_me_con_token_vencido_responde_401(client, make_user):
    user = make_user("player")
    vencido = jwt.encode(
        {"sub": str(user.id), "rol": "player", "exp": datetime.now(timezone.utc) - timedelta(seconds=1)},
        settings.jwt_secret,
        algorithm="HS256",
    )
    client.cookies.set(settings.session_cookie_name, vencido)
    assert client.get("/auth/me").status_code == 401


# ---------------------------------------------------------------------------
# Criterio: cerrar sesion borra la cookie
# ---------------------------------------------------------------------------
@requires_db
def test_logout_borra_la_cookie(client, make_user):
    user = make_user("player")
    _login(client, user.email)
    assert client.get("/auth/me").status_code == 200

    r = client.post("/auth/logout")
    assert r.status_code == 204
    assert 'Max-Age=0' in r.headers["set-cookie"]  # orden de borrado al navegador
    assert client.get("/auth/me").status_code == 401


@requires_db
def test_logout_sin_sesion_tambien_funciona(client):
    assert client.post("/auth/logout").status_code == 204


# ---------------------------------------------------------------------------
# PlayerUser (lo usaran HU-03 y HU-06): el admin recibe 403 (HU-12)
# ---------------------------------------------------------------------------
_probe = APIRouter()


@_probe.post("/_test/solo-jugador")
def _solo_jugador(user: PlayerUser):
    return {"ok": True}


@requires_db
def test_player_user_rechaza_al_admin(client, make_user):
    client.app.include_router(_probe)
    player, admin = make_user("player"), make_user("admin")

    _login(client, player.email)
    assert client.post("/_test/solo-jugador").status_code == 200

    _login(client, admin.email)
    assert client.post("/_test/solo-jugador").status_code == 403


# ---------------------------------------------------------------------------
# Herramienta de desarrollo: el usuario creado por la CLI puede iniciar sesion
# ---------------------------------------------------------------------------
@requires_db
def test_cli_crea_un_usuario_que_puede_iniciar_sesion(client, capsys):
    import uuid as _uuid

    from app.cli import main
    from app.db import SessionLocal
    from app.models import User

    correo = f"CLI-{_uuid.uuid4().hex[:8]}@Arqui.local"
    try:
        assert main(["crear-usuario", "--email", correo, "--nombre", "Desde CLI", "--password", "clave123"]) == 0
        assert main(["crear-usuario", "--email", correo.lower(), "--nombre", "Otra", "--password", "x"]) == 1  # duplicado
        r = _login(client, correo)
        assert r.status_code == 200 and r.json()["email"] == correo.lower() and r.json()["rol"] == "player"
    finally:
        with SessionLocal() as db:
            db.query(User).filter(User.email == correo.lower()).delete()
            db.commit()


# ===========================================================================
# HU-01: registro
# ===========================================================================

# ---------------------------------------------------------------------------
# Caso feliz
# ---------------------------------------------------------------------------
@requires_db
def test_registro_exitoso(client):
    """HU-01: crea usuario con rol player, saldo 0 y password hasheado."""
    from app.db import SessionLocal

    payload = _payload()
    response = client.post("/auth/register", json=payload)

    assert response.status_code == 201
    body = response.json()
    assert body["email"] == payload["email"]
    assert body["nombre"] == payload["name"]
    assert body["rol"] == ROLE_PLAYER
    assert "id" in body
    assert "password" not in body
    assert "password_hash" not in body

    with SessionLocal() as db:
        user = db.scalar(select(User).where(User.email == payload["email"]))
        assert user is not None
        assert user.password_hash != payload["password"]
        assert verify_password(payload["password"], user.password_hash)
        assert user.balance == 0
        assert user.role == ROLE_PLAYER

        db.delete(user)
        db.commit()


# ---------------------------------------------------------------------------
# Correo repetido
# ---------------------------------------------------------------------------
@requires_db
def test_registro_correo_repetido_devuelve_409(client):
    """HU-01: correo repetido -> 409 y mensaje exacto."""
    from app.db import SessionLocal

    payload = _payload()
    r1 = client.post("/auth/register", json=payload)
    assert r1.status_code == 201

    r2 = client.post("/auth/register", json=payload)
    assert r2.status_code == 409
    assert r2.json()["detail"] == "Este correo ya está registrado"

    with SessionLocal() as db:
        user = db.scalar(select(User).where(User.email == payload["email"]))
        if user:
            db.delete(user)
            db.commit()


@requires_db
def test_registro_correo_duplicado_por_mayusculas(client):
    """HU-01: "Ana@x.com" y "ana@x.com" son la misma cuenta (409)."""
    from app.db import SessionLocal

    base = _payload()
    email_upper = base["email"].upper()
    email_lower = base["email"].lower()

    r1 = client.post("/auth/register", json={**base, "email": email_upper})
    assert r1.status_code == 201

    r2 = client.post("/auth/register", json={**base, "email": email_lower})
    assert r2.status_code == 409
    assert r2.json()["detail"] == "Este correo ya está registrado"

    with SessionLocal() as db:
        user = db.scalar(select(User).where(User.email == email_lower))
        if user:
            db.delete(user)
            db.commit()


# ---------------------------------------------------------------------------
# Datos invalidos
# ---------------------------------------------------------------------------
@pytest.mark.parametrize(
    "override, motivo",
    [
        ({"email": "no-es-correo"}, "correo invalido"),
        ({"email": ""}, "correo vacio"),
        ({"name": ""}, "nombre vacio"),
        ({"password": "corta"}, "password muy corta"),
        ({"password": ""}, "password vacia"),
    ],
)
@requires_db
def test_registro_datos_invalidos_devuelven_422(client, override, motivo):
    """HU-01: campo vacio o correo invalido -> 422."""
    response = client.post("/auth/register", json=_payload(**override))
    assert response.status_code == 422, f"fallo: {motivo}"