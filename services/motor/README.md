# Motor de apuestas (Go)

Recibe los datos de carrera, maneja los mercados y las apuestas, y empuja todo lo que pasa en vivo al front por WebSocket. Es el único dueño de MongoDB: para mover saldo o puntos le pide a Pagos por REST.

## Estructura

```
main.go              # solo arma las piezas y arranca el servidor
internal/config      # variables de entorno (las obligatorias no tienen valor por defecto)
internal/mongodb     # conexión a MongoDB
internal/httpx       # JSON y CORS compartidos
internal/health      # GET /health (503 si MongoDB no responde)
internal/auth        # valida el JWT de Pagos; middleware REST y WebSocket
internal/liquidacion # HU-07: cierre, observación y liquidación reintentable
internal/ws          # hub de WebSocket: GET /ws, Difundir y EnviarA
```

Para agregar una HU, crea un paquete en `internal/` con una función `Register(mux, ...)` y llámala desde `main.go`. Así nadie edita el paquete de otro:

```go
func Register(mux *http.ServeMux, db *mongo.Database, hub *ws.Hub, a *auth.Autenticador) {
	mux.Handle("POST /mercados", a.ExigirRol(auth.RolAdmin, http.HandlerFunc(abrir)))
}
```

## Autenticación (RNF-06)

Pagos emite el JWT al iniciar sesión y lo guarda en una cookie HttpOnly. El motor **no le pregunta a Pagos**: verifica la firma por su cuenta con el secreto compartido `JWT_SECRET`.

Contrato acordado (en `internal/auth/contrato.go`, **pendiente de confirmar con Pagos**):

| Qué | Valor |
| --- | --- |
| Algoritmo | `HS256` (cualquier otro, incluido `none`, se rechaza) |
| Cookie | `session` |
| Claim del usuario | `sub`: UUID como string |
| Claim del rol | `rol`: `player` o `admin` |
| Vencimiento | `exp` obligatorio (Pagos lo fija con `JWT_EXPIRE_MINUTES`) |

Cómo proteger una ruta:

```go
mux.Handle("POST /apuestas", a.ExigirSesion(http.HandlerFunc(apostar)))                // 401 sin sesión
mux.Handle("POST /mercados", a.ExigirRol(auth.RolAdmin, http.HandlerFunc(abrirMercado))) // 403 si no es admin
```

Dentro del handler, `sesion, _ := auth.SesionDe(r.Context())` entrega `sesion.UserID` y `sesion.Rol`.

## Variables de entorno

| Variable | Obligatoria | Ejemplo |
| --- | --- | --- |
| `MONGO_URI` | sí | `mongodb://arqui:arqui_pass@mongo:27017/apuestas_db?authSource=admin` |
| `MONGO_DB` | sí | `apuestas_db` |
| `JWT_SECRET` | sí | el mismo que usa Pagos |
| `PAGOS_BASE_URL` | sí | `http://pagos:8000` |
| `INTERNAL_API_TOKEN` | sí | Token compartido con Pagos para `/internal/*` |
| `ALLOWED_ORIGINS` | sí | `http://localhost:5173,http://127.0.0.1:5173` |
| `HTTP_PORT` | no (8080) | `8080` |
| `BETTING_WINDOW_SECONDS` | no (30) | `30` |
| `OBSERVATION_WINDOW_SECONDS` | no (60) | `60` |

Si falta una obligatoria, el motor no arranca y el log dice cuál falta.
`PAGOS_MOCK=true` desactiva HU-07: una apuesta simulada no tiene registro
financiero en PostgreSQL y no puede liquidarse en el ledger.

## Liquidación HU-07 (demo limitada)

El worker consulta cada dos segundos los mercados vencidos, cierra los que
superaron `cierra_en`, espera hasta `observa_hasta` y solo resuelve
`tipo: "driver_first_position"`. Filtra los eventos por `timestamp` de carrera
en el intervalo inclusivo `[cierra_en, observa_hasta]` y exige que `leido_en`
también esté dentro del intervalo para descartar cargas tardías. Solo combina
posiciones con timestamp igual al milisegundo y elige el instante más reciente
que tenga una posición válida para cada piloto del roster; no combina filas
tomadas en tiempos incompatibles. La muestra no se considera una clasificación
oficial final. Ante datos ausentes, ambiguos o una falla de servicio conserva
el mercado cerrado, registra `liquidacion_error` en MongoDB y reintenta. Los
contratos y límites completos están en
[`../../documentation/hu-07-liquidacion-apuestas.md`](../../documentation/hu-07-liquidacion-apuestas.md).
HU-04 no ingiere datos continuamente: durante la observación se debe invocar
el endpoint existente `POST /ingest/openf1`; no hay un job automático que
consuma OpenF1 en vivo.

## Mensajes del WebSocket

El front se conecta a `ws://localhost:8080/ws`. La conexión exige la cookie `session` con un JWT válido (si no, 401) y un origen de `ALLOWED_ORIGINS` (si no, 403). El navegador envía la cookie solo; el front no tiene que hacer nada extra.

Todo mensaje que envía el motor tiene la misma forma:

```json
{ "type": "mercado_abierto", "payload": { ... } }
```

El front decide qué hacer según `type`. Los tipos están en `internal/ws/mensajes.go`; si agregas uno, ponlo ahí y en esta tabla.

| `type` | A quién | HU | Cuándo |
| --- | --- | --- | --- |
| `posiciones` | todos | HU-05 | cambia la posición de un piloto |
| `mercado_abierto` | todos | HU-11 | el administrador abre un mercado |
| `mercado_cerrado` | todos | HU-11 | termina la ventana de apuestas (empieza la observación) |
| `mercado_liquidado` | todos | HU-07 | el mercado se resolvió |
| `resultado_apuesta` | solo al usuario | HU-08 | se liquidó la apuesta de ese usuario |

Desde Go:

```go
hub.Difundir(ws.Mensaje{Type: ws.TipoMercadoAbierto, Payload: mercado})
hub.EnviarA(userID, ws.Mensaje{Type: ws.TipoResultadoApuesta, Payload: resultado})
```

El `payload` de cada tipo lo define quien implementa la HU; documéntalo aquí cuando quede listo. Propuesta para `resultado_apuesta` (HU-08):

```json
{
  "type": "resultado_apuesta",
  "payload": {
    "mercado_id": "…",
    "apuesta_id": "…",
    "resultado": "ganada",
    "puntos_obtenidos": 60,
    "puntaje_total": 120,
    "saldo_tokens": 400
  }
}
```

Por ahora el front no envía mensajes por el WebSocket: las acciones (apostar, abrir mercado) van por REST. Si la conexión se cae, el front debe reconectarse solo (HU-05).

## Correr y probar

```bash
docker compose up --build motor      # desde la raíz del repo
cd services/motor && gofmt -l . && go vet ./... && go test ./...
```
