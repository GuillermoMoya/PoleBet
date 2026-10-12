"""Autenticación compartida para las rutas internas de servicio."""
import hmac

from app.core.config import settings


def token_servicio_valido(token: str | None) -> bool:
    if not token:
        return False
    return hmac.compare_digest(
        token.encode("utf-8"), settings.internal_api_token.encode("utf-8")
    )
