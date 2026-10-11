"""Endpoints de sesion: login, logout y "quien soy" (HU-02, RF-02, RNF-06).

    POST /auth/login   correo + contraseña -> cookie de sesion con el JWT
    POST /auth/logout  borra la cookie (siempre responde 204)
    GET  /auth/me      usuario de la cookie, o 401 si no hay sesion valida

El JWT nunca viaja en el cuerpo de la respuesta: solo en la cookie HttpOnly.
El front no lo ve ni lo necesita; el navegador lo envia solo (credentials).
"""
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Response, status

from app.core.security import create_access_token
from app.core.session import borrar_cookie_sesion, poner_cookie_sesion
from app.deps import CurrentUser, DbSession
from app.schemas.auth import LoginIn, UsuarioOut
from app.services.auth import ProveedorIdentidad, get_proveedor_identidad

router = APIRouter(prefix="/auth", tags=["auth"])

# Mensaje unico para cualquier credencial invalida. No dice si fallo el correo
# o la contraseña, para no revelar que correos estan registrados (HU-02).
MENSAJE_CREDENCIALES = "Correo o contraseña incorrectos"


def _sin_cache(response: Response) -> None:
    # Respuestas con datos de sesion: que ningun proxy ni el navegador las guarde.
    response.headers["Cache-Control"] = "no-store"


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
