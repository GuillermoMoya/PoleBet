"""Cookie de sesion (HU-02, RNF-06).

Un solo lugar decide como se escribe y se borra la cookie que lleva el JWT.
Si mañana cambia algun atributo (Secure en produccion, dominio, etc.), se
cambia aqui y nada mas.

Por que estos atributos:

- HttpOnly: el JavaScript del front NO puede leer el token. Si alguien logra
  inyectar un script (XSS), no se puede robar la sesion.
- SameSite=Lax: el navegador no envia la cookie en peticiones que vienen de
  otro sitio (protege contra CSRF). localhost:5173 (front), localhost:8000
  (Pagos) y localhost:8080 (motor) cuentan como el MISMO sitio, porque
  SameSite no mira el puerto: por eso la cookie que pone Pagos tambien le llega
  al motor, que la valida al abrir el WebSocket.
- Secure: solo por HTTPS. En la demo local (http) va en False; se activa con
  SESSION_COOKIE_SECURE=true cuando haya HTTPS.
- Max-Age igual a la vida del JWT: la cookie desaparece cuando el token ya no
  sirve, asi el navegador no guarda una sesion muerta.
"""
from fastapi import Response

from app.core.config import settings

# Atributos que deben ser identicos al crear y al borrar la cookie; si no
# coinciden, el navegador trata el borrado como otra cookie y no la elimina.
_ATRIBUTOS_COMUNES = {
    "path": "/",
    "httponly": True,
    "samesite": "lax",
}


def poner_cookie_sesion(response: Response, token: str) -> None:
    """Agrega la cabecera Set-Cookie con el JWT de sesion."""
    response.set_cookie(
        key=settings.session_cookie_name,
        value=token,
        max_age=settings.jwt_expire_minutes * 60,
        secure=settings.session_cookie_secure,
        **_ATRIBUTOS_COMUNES,
    )


def borrar_cookie_sesion(response: Response) -> None:
    """Le pide al navegador que elimine la cookie de sesion (logout)."""
    response.delete_cookie(
        key=settings.session_cookie_name,
        secure=settings.session_cookie_secure,
        **_ATRIBUTOS_COMUNES,
    )
