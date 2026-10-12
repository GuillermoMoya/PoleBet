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
internal/apuestas    # HU-06: POST /apuestas
internal/mercados    # HU-11: abrir mercado y ciclo abierto → cerrado → liquidar
internal/ingest      # HU-04: ingesta de OpenF1 y replay
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

## Liquidación HU-07

Hay un solo tipo de mercado, Sí/No: **«¿Habrá algún cambio de posición durante
la ventana de observación?»** (`tipo: "cambio_posicion"`). El worker
(`internal/liquidacion`) consulta cada 2 s los mercados ya `cerrado` cuyo
`observa_hasta` pasó (el cierre es de HU-11) y decide:

- **Sí** si la reproducción de la carrera (`POST /ingest/replay`) emitió algún
  evento de posición con `emitido_en` en `[cierra_en, observa_hasta]`.
- **No** si no emitió ninguno y la reproducción estuvo viva toda la ventana
  (latido en la colección `reproducciones`).
- Si no hay eventos ni latido, el mercado queda `cerrado`, registra
  `liquidacion_error` en MongoDB y reintenta. Lo mismo ante una falla de Pagos
  o de MongoDB.

`emitido_en` y el latido son horas reales que escribe la reproducción
(`internal/ingest/replay.go`); el `timestamp` de los eventos es histórico y no
se compara con la ventana. Para la demo: ingesta una vez, lanza
`POST /ingest/replay` con `factor: 1` y déjala corriendo mientras se abren
mercados. La regla completa, el contrato con Pagos y las decisiones están en
[`../../documentation/hu-07-liquidacion-apuestas.md`](../../documentation/hu-07-liquidacion-apuestas.md).

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

El `payload` de cada tipo lo define quien implementa la HU; documéntalo aquí cuando quede listo.

`mercado_abierto` y `mercado_cerrado` (HU-11) llevan el mercado completo, igual que `GET /mercados/actual`:

```json
{
  "type": "mercado_abierto",
  "payload": {
    "id": "6708f1c2a1b2c3d4e5f60718",
    "carrera_id": "2024_monza",
    "pregunta": "¿Habrá algún cambio de posición durante la ventana de observación?",
    "opciones": ["Sí", "No"],
    "estado": "abierto",
    "abre_en": "2026-10-11T15:00:00Z",
    "cierra_en": "2026-10-11T15:00:30Z",
    "observa_hasta": "2026-10-11T15:01:30Z",
    "ahora": "2026-10-11T15:00:00.012Z"
  }
}
```

`ahora` es la hora del servidor al enviar el mensaje: el front calcula con ella el desfase de su reloj para que la cuenta regresiva no dependa del navegador. Igual, quien decide el cierre es el motor (RNF-05). Se propone que `mercado_liquidado` (HU-07) use el mismo payload con `estado: "liquidado"`; el front ya lo escucha así.

Propuesta para `resultado_apuesta` (HU-08):

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

## Mercados (HU-11)

| Ruta | Quién | Respuesta |
| --- | --- | --- |
| `POST /mercados` | solo admin (401 sin sesión, 403 si no es admin) | `201` con el mercado ya abierto |
| `GET /mercados/actual` | cualquier sesión | `200` con el mercado más reciente (en el estado que esté) o `null` si nunca se abrió uno |

Cuerpo de `POST /mercados`:

```json
{ "pregunta": "¿Habrá algún cambio de posición durante la ventana de observación?", "opciones": ["Sí", "No"], "carrera_id": "2024_monza" }
```

- `carrera_id` es opcional: si no viene se usa la última carrera cargada en `carreras` (HU-04).
- El único tipo de mercado es Sí/No (`cambio_posicion`, lo fija el motor): las opciones deben ser **exactamente `["Sí", "No"]`**, en ese orden, o responde `400`. El front las envía prellenadas.
- Pregunta de 1 a 200 caracteres. Su texto no se valida contra la regla: la liquidación se guía solo por el tipo.
- Errores: `400` validación (el mensaje dice qué corregir), `404` carrera inexistente, `409` ya hay un mercado abierto, `422` no hay ninguna carrera cargada.

Ciclo, siempre con el reloj del servidor:

1. Al abrirlo se guardan `abre_en`, `cierra_en = abre_en + BETTING_WINDOW_SECONDS` y `observa_hasta = cierra_en + OBSERVATION_WINDOW_SECONDS`, y se difunde `mercado_abierto`.
2. En `cierra_en` pasa a `cerrado` y se difunde `mercado_cerrado`. HU-06 además compara `cierra_en` con su propio reloj, así que una apuesta que llega tarde se rechaza aunque el temporizador se atrase.
3. Desde `observa_hasta`, el worker de HU-07 (`liquidacion.Worker`, registrado en `main.go`) liquida el mercado. No usa el gancho `Servicio.Liquidador`: consulta cada 2 s los mercados ya `cerrado` con observación terminada, toma un lease en MongoDB para que dos workers no liquiden el mismo mercado y reintenta si Pagos o MongoDB fallan. Es idempotente, así que tras un reinicio retoma los mercados cerrados sin liquidar. Si `PAGOS_MOCK=true` el worker no arranca y el mercado solo queda en `cerrado`.

Solo puede haber un mercado abierto a la vez: lo impide el servicio y, además, un índice único parcial (`uq_un_mercado_abierto`) que el motor crea al arrancar. Si el motor se reinicia, retoma los mercados pendientes: cierra los que vencieron mientras estaba apagado y reprograma los demás.

Probarlo a mano (con el JWT de admin del README de la raíz en `$TOKEN`):

```bash
curl -X POST http://localhost:8080/mercados -H "Cookie: session=$TOKEN" -H "Content-Type: application/json" \
  -d '{"pregunta":"¿Habrá algún cambio de posición durante la ventana de observación?","opciones":["Sí","No"]}'
curl http://localhost:8080/mercados/actual -H "Cookie: session=$TOKEN"
```

## Correr y probar

```bash
docker compose up --build motor      # desde la raíz del repo
cd services/motor && gofmt -l . && go vet ./... && go test ./...
```
