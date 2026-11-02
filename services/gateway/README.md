# API Gateway (T2-01)

**API Gateway** (Nginx). Dueño: **Tomás**. Única entrada web de los jugadores y
uno de los dos únicos puertos publicados al host (80).

## Qué hace

Toda la configuración está en `nginx.conf`. Enruta por prefijo:

| Ruta pública   | Destino      | Sesión        |
| -------------- | ------------ | ------------- |
| `/api/auth/`   | Usuarios     | no (login/logout) |
| `/api/ws`      | Motor        | protegida (WebSocket) |
| `/api/`        | Orquestador  | protegida (se quita el prefijo `/api`) |
| `/`            | Front        | no (SPA y assets) |
| `/health`      | —            | healthcheck del propio Gateway |

En las rutas protegidas:

- Hace `auth_request` a Usuarios (`GET /internal/auth`); sin sesión válida → 401.
- **Neutraliza** `X-User-Id` / `X-User-Rol` en toda petición y reinyecta solo los
  que devuelve Usuarios, para que el cliente no pueda suplantar a otro (RNF-17).
- El WebSocket usa `proxy_read_timeout 1h`, así no se corta a los 60 s.

## Redes

Vive en **borde** (de cara al host) e **interna** (para alcanzar Usuarios,
Orquestador, Motor y Front). No toca ninguna base de datos.

## Pendiente para cerrar (dependencia de Usuarios, Misael)

El `auth_request` apunta a `GET /internal/auth` de Usuarios, que todavía no
existe (HU2-02). Hasta que Misael lo entregue devolviendo 200 con `X-User-Id` /
`X-User-Rol` o 401, las rutas protegidas no se pueden validar de verdad.

Para probar el Gateway mientras tanto, `nginx.conf` trae un bloque `/_auth` mock
comentado: se activa comentando el `location = /_auth` real y descomentando el
mock (acepta cualquier sesión con un jugador fijo). **Debe volver a quedar el
real antes de cerrar T2-01.**
