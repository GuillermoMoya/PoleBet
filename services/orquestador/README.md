# Orquestador (Go)

Coordina los flujos que tocan varios servicios: registro, página del usuario (SSR), compra de tokens, calendario, inscripción y apuesta. Llama a Usuarios, Pagos y Motor en orden, responde un solo resultado y reintenta lo idempotente cuando un paso falla. Solo recibe tráfico del API Gateway.

No valida el JWT (lo hace el Gateway), no tiene base de datos y ningún otro servicio lo llama. El contrato completo de las rutas está en [`documentation/orquestador-api.md`](../../documentation/orquestador-api.md).

## Estructura

```
main.go           # solo arma las piezas, arranca el servidor y lo apaga limpio (Ctrl+C / SIGTERM)
internal/config   # variables de entorno (las obligatorias no tienen valor por defecto)
internal/httpx    # EscribirJSON
internal/health   # GET /health
internal/billetera # GET /billetera, POST /billetera/compras (HU2-04)
internal/errores  # formato {error, codigo} y la tabla de errores del contrato
internal/sesion   # middleware: X-User-Id y X-User-Rol del Gateway → 401 / 403
internal/traza    # X-Request-Id de cada petición
internal/interno  # cliente HTTP hacia Usuarios, Pagos y Motor (token, timeout, reintentos)
```

Cada flujo va en su propio paquete de `internal/` con una función `Register(mux, ...)` que se llama desde `main.go`. Las rutas de jugador se envuelven con el middleware de sesión; las públicas (`/health`, `/registro`) no:

```go
mux.Handle("POST /apuestas", sesion.ExigirJugador(http.HandlerFunc(apostar)))
```

## Variables de entorno

| Variable | Obligatoria | Uso |
| --- | --- | --- |
| `USUARIOS_BASE_URL` | sí | URL de Usuarios, p. ej. `http://usuarios:8001` |
| `PAGOS_BASE_URL` | sí | URL de Pagos, p. ej. `http://pagos:8000` |
| `MOTOR_BASE_URL` | sí | URL del Motor, p. ej. `http://motor:8080` |
| `INTERNAL_API_TOKEN` | sí | Token de servicio, mínimo 32 caracteres; el mismo que usan Pagos y el Motor |
| `HTTP_PORT` | no | Puerto de escucha; por defecto `8081` |

Si falta una obligatoria, el servicio no arranca y el error dice cuáles faltan.

## Correrlo

Con Go 1.25 o superior, desde `services/orquestador`:

```bash
export USUARIOS_BASE_URL=http://localhost:8001
export PAGOS_BASE_URL=http://localhost:8000
export MOTOR_BASE_URL=http://localhost:8080
export INTERNAL_API_TOKEN=$(openssl rand -hex 32)   # o el mismo valor que tengan Pagos y el Motor
go run .
```

Pruebas:

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

## Probarlo con curl

```bash
curl -i localhost:8081/health
# HTTP/1.1 200 OK
# X-Request-Id: 3f0c…        (generado; si mandas -H "X-Request-Id: algo", vuelve el mismo)
# {"estado":"ok"}
```

Sin Gateway, las rutas de jugador se prueban poniendo a mano los headers que él agregaría:

```bash
curl -i localhost:8081/eventos \
  -H "X-User-Id: 0b6c2f1e-8d4a-4c3b-9f2e-1a2b3c4d5e6f" \
  -H "X-User-Rol: player"
```

Sin esos headers la respuesta es `401 {"error":"No hay sesión","codigo":"sin_sesion"}`, y con `X-User-Rol: admin` es `403 solo_jugadores`. Las rutas de cada flujo (`/eventos`, `/apuestas`, …) se agregan una por una; mientras no exista una ruta, el servidor responde 404.
