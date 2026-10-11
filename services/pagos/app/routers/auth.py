"""Endpoints de autenticacion.

    POST /auth/register  registro de jugador (HU-01)
    POST /auth/login     correo + contrasena -> cookie de sesion con el JWT (HU-02)
    POST /auth/logout    borra la cookie (siempre responde 204) (HU-02)
    GET  /auth/me        usuario de la cookie, o 401 si no hay sesion valida (HU-02)

El JWT nunca viaja en el cuerpo de la respuesta: solo en la cookie HttpOnly.
El front no lo ve ni lo necesita; el navegador lo envia solo (credentials).
"""
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Response, status

from app.core.security import create_access_token
from app.core.session import borrar_cookie_sesion, poner_cookie_sesion
from app.deps import CurrentUser, DbSession
from app.schemas.auth import LoginIn, RegisterRequest, UsuarioOut
from app.services.auth import (
    ProveedorIdentidad,
    get_proveedor_identidad,
    registrar_usuario,
)

router = APIRouter(prefix="/auth", tags=["auth"])

# Mensaje unico para cualquier credencial invalida. No dice si fallo el correo
# o la contraseña, para no revelar que correos estan registrados (HU-02).
MENSAJE_CREDENCIALES = "Correo o contraseña incorrectos"


def _sin_cache(response: Response) -> None:
    # Respuestas con datos de sesion: que ningun proxy ni el navegador las guarde.
    response.headers["Cache-Control"] = "no-store"


# ---------------------------------------------------------------------------
# HU-01: registro
# ---------------------------------------------------------------------------
@router.post(
    "/register",
    status_code=status.HTTP_201_CREATED,
    response_model=UsuarioOut,
    summary="Registrar un jugador (HU-01)",
)
def register(data: RegisterRequest, db: DbSession) -> UsuarioOut:
    """Crea un jugador con rol 'player' y saldo 0.

    - 201: devuelve el usuario creado (sin contrasena).
    - 409: si el correo ya esta registrado (comparacion case-insensitive).
    - 422: si algun campo es invalido (nombre vacio, correo mal formado,
      contrasena demasiado corta).
    """
    user = registrar_usuario(db, data)
    return UsuarioOut.desde_modelo(user)


# ---------------------------------------------------------------------------
# HU-02: login, logout, me
# ---------------------------------------------------------------------------
@router.post(
    "/login",
    response_model=UsuarioOut,
    responses={401: {"description": MENSAJE_CREDENCIALES}},
)
def login(
    datos: LoginIn,
    response: Response,
    db: DbSession,
    proveedor: Annotated[ProveedorIdentidad, Depends(get_proveedor_identidad)],
) -> UsuarioOut:
    """Valida las credenciales y abre la sesion.

    - 200: devuelve el usuario y pone la cookie `session` con el JWT
      (claims `sub` = id, `rol` = player|admin, `exp`), el mismo contrato que
      valida el motor en services/motor/internal/auth/contrato.go.
    - 401: "Correo o contraseña incorrectos".
    """
    user = proveedor.autenticar(db, datos.email, datos.password)
    if user is None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail=MENSAJE_CREDENCIALES)

    poner_cookie_sesion(response, create_access_token(user.id, user.role))
    _sin_cache(response)
    return UsuarioOut.desde_modelo(user)


@router.post("/logout", status_code=status.HTTP_204_NO_CONTENT)
def logout(response: Response) -> None:
    """Cierra la sesion borrando la cookie.

    No exige sesion: si la cookie ya vencio, el usuario igual debe poder
    "salir" sin ver un error. Como el JWT no se guarda en el servidor, no hay
    nada mas que invalidar (si algun dia se necesita revocar tokens antes de
    que venzan, se agrega una lista negra; queda como deuda tecnica).
    """
    borrar_cookie_sesion(response)
    _sin_cache(response)


@router.get("/me", response_model=UsuarioOut, responses={401: {"description": "No hay sesion valida"}})
def me(user: CurrentUser, response: Response) -> UsuarioOut:
    """Devuelve el usuario de la cookie. El front lo llama al cargar la app
    para saber si ya hay una sesion abierta (por ejemplo, tras recargar)."""
    _sin_cache(response)
    return UsuarioOut.desde_modelo(user)