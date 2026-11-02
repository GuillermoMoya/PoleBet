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
internal/posiciones  # HU-05: tablero de posiciones, GET /posiciones y difusión de la foto
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
| `REDIS_URL` | no (torneo off) | `redis://redis:6379/0` |
| `EVENT_INITIAL_CHIPS` | no (1000) | `1000` |
| `EVENT_COMMISSION_PCT` | no (10) | `10` |
| `EVENT_MIN_WAGER_PCT` | no (50) | `50` |

Si falta una obligatoria, el motor no arranca y el log dice cuál falta. Sin
`REDIS_URL` el motor arranca igual, pero las apuestas de torneo no se liquidan
en fichas (quedan reintentables): el torneo (HU2-11) necesita Redis.

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
mercados. Lanza la reproducción ANTES de abrir mercados y no la relances con un mercado abierto o en observación; ese mercado no podría liquidarse como "No". La regla completa, el contrato con Pagos y las decisiones están en
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
| `posiciones` | todos | HU-05 | arranca la reproducción y cada vez que un piloto cambia de posición (foto completa) |
| `mercado_abierto` | todos | HU-11 | el administrador abre un mercado |
| `mercado_cerrado` | todos | HU-11 | termina la ventana de apuestas (empieza la observación) |
| `mercado_liquidado` | todos | HU-07 | el mercado se resolvió |
| `resultado_apuesta` | solo al usuario | HU-08 / HU2-11 | se liquidó la apuesta de ese usuario (puntos en individual, fichas en torneo) |
| `ranking_torneo` | todos | HU2-11 | cambiaron las fichas de un torneo (tras liquidar sus apuestas) |

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

`resultado_apuesta` (HU-08) se envía solo al dueño de la apuesta, una vez por apuesta, justo después de guardar su resultado en MongoDB:

```json
{
  "type": "resultado_apuesta",
  "payload": {
    "mercado_id": "6708f1c2a1b2c3d4e5f60718",
    "apuesta_id": "6708f1d0a1b2c3d4e5f60720",
    "opcion": "Sí",
    "opcion_ganadora": "Sí",
    "gano": true,
    "puntos_obtenidos": 12.5,
    "puntaje_total": 132.5
  }
}
```

| Campo | Qué es |
| --- | --- |
| `opcion` / `opcion_ganadora` | lo que apostó el jugador y lo que ganó el mercado |
| `gano` | `opcion == opcion_ganadora` |
| `puntos_obtenidos` | `10 + monto × 0,5` si ganó, `0` si perdió; puede tener decimales |
| `puntaje_total` | puntos acumulados del jugador después de esta apuesta (`points_total` de Pagos) |

No lleva saldo porque los tokens apostados no se devuelven y la liquidación no cambia el saldo. Si el jugador no está conectado o el envío falla, el motor solo lo registra en el log: la liquidación sigue y no se reintenta por eso. En un reintento del mercado, las apuestas que ya estaban liquidadas no se vuelven a notificar. Todos los `resultado_apuesta` de un mercado salen antes de su `mercado_liquidado`, así que el front puede concluir que el jugador no apostó si recibe `mercado_liquidado` sin un resultado previo.

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
3. Desde `observa_hasta`, el worker de HU-07 (`liquidacion.Worker`, registrado en `main.go`) liquida el mercado. No usa el gancho `Servicio.Liquidador`: consulta cada 2 s los mercados ya `cerrado` con observación terminada, toma un lease en MongoDB para que dos workers no liquiden el mismo mercado y reintenta si Pagos o MongoDB fallan. Es idempotente, así que tras un reinicio retoma los mercados cerrados sin liquidar.

Solo puede haber un mercado abierto a la vez: lo impide el servicio y, además, un índice único parcial (`uq_un_mercado_abierto`) que el motor crea al arrancar. Si el motor se reinicia, retoma los mercados pendientes: cierra los que vencieron mientras estaba apagado y reprograma los demás.

Probarlo a mano (con el JWT de admin del README de la raíz en `$TOKEN`):

```bash
curl -X POST http://localhost:8080/mercados -H "Cookie: session=$TOKEN" -H "Content-Type: application/json" \
  -d '{"pregunta":"¿Habrá algún cambio de posición durante la ventana de observación?","opciones":["Sí","No"]}'
curl http://localhost:8080/mercados/actual -H "Cookie: session=$TOKEN"
```

## Posiciones en vivo (HU-05)

La reproducción (`POST /ingest/replay`, ver `internal/ingest/README.md`) no difunde nada por sí misma: avisa a un `ingest.ObservadorCarrera`. En `main.go` ese observador es el `posiciones.Difusor`, que mantiene en memoria un `Tablero` con la carrera en curso y difunde la **foto completa** (todos los pilotos, ordenados) en un mensaje `posiciones`:

1. Al lanzar la reproducción, el motor reconstruye dónde iba cada piloto en el punto de inicio (`desde`): para cada piloto, su última posición con `timestamp < desde`. Los datos de cada piloto salen del evento `lista_pilotos` de la ingesta. Difunde esa foto de una vez.
2. Por cada evento de posición que emite la reproducción, actualiza el tablero y difunde la foto nueva.
3. Al terminar la reproducción (fin de carrera o 30 min) la última foto se queda como está.

Payload de `posiciones`, que también es la respuesta de `GET /posiciones` (sin `emitido_en`):

```json
{
  "type": "posiciones",
  "payload": {
    "carrera_id": "2024_monza",
    "actualizado_en": "2026-10-12T15:00:41.207Z",
    "emitido_en": "2026-10-12T15:00:41.207Z",
    "pilotos": [
      { "posicion": 1, "numero": 16, "codigo": "LEC", "nombre": "Charles LECLERC", "equipo": "Ferrari", "color_equipo": "#E80020", "diferencia": "Líder" },
      { "posicion": 2, "numero": 81, "codigo": "PIA", "nombre": "Oscar PIASTRI", "equipo": "McLaren", "color_equipo": "#FF8000", "diferencia": "—" }
    ]
  }
}
```

| Campo | Qué es |
| --- | --- |
| `actualizado_en` | hora **real** del servidor al aplicar el cambio. No es el `timestamp` histórico: el front descarta fotos más viejas que la que tiene, y con la hora de 2024 relanzar la reproducción desde un punto anterior congelaría la tabla |
| `emitido_en` | hora real del envío por WebSocket, para medir la latencia (RNF-04) |
| `color_equipo` | `team_colour` de OpenF1 con `#`; se omite si OpenF1 no lo da |
| `diferencia` | `"Líder"` para P1 y `"—"` para el resto: no se ingieren intervalos (OpenF1 `/intervals` queda como mejora) |
| `codigo` / `nombre` | si un piloto no está en `lista_pilotos`, sale con su número como código y sin nombre |

No hay `vuelta`, porque OpenF1 `/position` no la trae. Si dos pilotos tienen un instante la misma posición (OpenF1 manda los dos lados de un adelantamiento en eventos separados), salen los dos con ella y el siguiente evento lo corrige.

| Ruta | Quién | Respuesta |
| --- | --- | --- |
| `GET /posiciones` | cualquier sesión (401 sin sesión) | `200` con la última foto, o `404 {"error": "no hay una carrera en curso"}` si no se ha lanzado ninguna reproducción desde que arrancó el motor |

El mensaje `evento_carrera` (cada evento suelto de la reproducción) **ya no se difunde**: nadie lo usaba y duplicaba el tráfico del WebSocket.

**Después de actualizar a esta versión, vuelve a correr la ingesta** (`POST /ingest/openf1`, README de la raíz): `team_colour` se agregó con HU-05 y las carreras ingeridas antes no lo tienen (la tabla sale sin colores). La ingesta es idempotente. Hazlo antes de lanzar la reproducción, nunca con una en curso.

## Fichas y ranking del torneo (HU2-11)

Las **fichas** del torneo son distintas de los tokens: se reparten al inscribirse
(1000 por jugador), solo sirven dentro del evento y **viven en Redis**, del que
el motor es el único dueño (RNF-18). En PostgreSQL de Pagos solo queda el
resultado final (`chips_final`), que escribe el cierre del evento (HU2-12).

El paquete `internal/fichas` es el almacén de fichas y el ranking:

- **Inicializar** (`Almacen.InicializarTorneo`): al largar el torneo, le da a
  cada jugador sus fichas iniciales. Lo alimenta la largada de Pagos
  (`POST /internal/events/{id}/start`, HU2-06), que devuelve la lista de
  inscritos. Es idempotente: repetir la largada no reinicia a nadie.
- **Reservar** (`Almacen.ReservarApuesta`): al apostar en modo torneo (HU2-09)
  se descuentan las fichas. No se restan «a mano»: se guarda la reserva por
  apuesta y se recalcula el total, así un reintento con la misma apuesta no
  descuenta dos veces (RNF-12). Quien llega a 0 fichas no puede apostar.
- **Liquidar** (`Almacen.Liquidar`): al resolver el mercado, una apuesta de
  torneo ganadora recibe `monto × multiplicador` en fichas y una perdedora
  nada. El resultado se escribe una sola vez por apuesta y el total se
  recalcula: repetir la liquidación no paga doble.

**Regla de oro de idempotencia:** las fichas nunca se suman con `INCRBY`. Cada
apuesta escribe su resultado una vez (clave por apuesta) y el saldo del jugador
se **recalcula** desde su saldo inicial, sus reservas y los resultados escritos.
Así el componente es seguro ante reintentos, reinicios del motor y dobles
entregas del WebSocket.

El worker de liquidación (HU-07) decide por el `modo` de cada contexto de
apuesta: `individual` va a Pagos en tokens (como siempre); `torneo` va al
almacén de fichas y no toca Pagos. Si no hay `REDIS_URL`, una apuesta de torneo
queda reintentable (nunca se liquida un torneo en tokens).

Al cambiar las fichas de un torneo, el motor difunde el **ranking** por
WebSocket (RF-18, cambio en pantalla en < 1 s, RNF-13):

```json
{
  "type": "ranking_torneo",
  "payload": {
    "evento_id": "b3f1…",
    "min_apostado": 500,
    "botin": 400,
    "actualizado_en": "2026-10-31T15:10:00.123Z",
    "emitido_en": "2026-10-31T15:10:00.124Z",
    "filas": [
      { "puesto": 1, "usuario_id": "u2", "fichas": 1450, "apostado": 600, "avance_pct": 100, "elegible": true },
      { "puesto": 2, "usuario_id": "u1", "fichas": 700, "apostado": 300, "avance_pct": 60, "elegible": false }
    ]
  }
}
```

| Campo | Qué es |
| --- | --- |
| `puesto` | lo comparten los empatados y el siguiente grupo toma el puesto que sigue (p. ej. `1,1,2`), igual que el reparto del botín (HU2-12) |
| `fichas` | saldo actual de fichas del jugador |
| `apostado` | fichas comprometidas en apuestas; no baja aunque la apuesta pierda |
| `avance_pct` | `apostado` frente al mínimo para ser elegible (0..100, topado en 100) |
| `elegible` | ya apostó el mínimo (`EVENT_MIN_WAGER_PCT` de las fichas iniciales) para entrar al reparto |
| `min_apostado` | fichas que hay que apostar para ser elegible (techo del porcentaje) |

Cuando la apuesta liquidada fue de torneo, `resultado_apuesta` (que llega solo
al dueño, HU-08) trae además `modo: "torneo"`, `fichas_ganadas`, `fichas_total`
y `puesto` en lugar de puntos (RF-17).

Detalle completo, claves de Redis y decisiones en
[`../../documentation/hu2-11-fichas-ranking.md`](../../documentation/hu2-11-fichas-ranking.md).

**Cierre del torneo (HU2-12).** `Almacen.CerrarTorneo(evento, pagos)` lee las
fichas finales de Redis, las envía a Pagos (`POST /internal/events/{id}/close`,
cliente `fichas.PagosCierreHTTP`) y solo si Pagos confirma el reparto borra las
claves del torneo. Si Pagos falla, Redis queda intacto y el cierre se puede
repetir (Pagos es idempotente). Lo invoca el flujo de fin de carrera (HU2-07).
Ver [`../../documentation/hu2-12-reparto-botin.md`](../../documentation/hu2-12-reparto-botin.md).

## Correr y probar

```bash
docker compose up --build motor      # desde la raíz del repo
cd services/motor && gofmt -l . && go vet ./... && go test ./...
```

`TestEstadoInicialMongo` (`internal/ingest`) prueba la reconstrucción de posiciones de HU-05 contra un MongoDB real y se salta si no existe `MOTOR_TEST_MONGO_URI`. Usa la base `motor_test_hu05`, que borra al terminar; no toca `apuestas_db`. Con el stack levantado:

```bash
docker run --rm --network arquisoft-prototipo1_arqui_net -v "$PWD":/app -w /app \
  -e MOTOR_TEST_MONGO_URI='mongodb://arqui:arqui_pass@arqui_mongo:27017/?authSource=admin' \
  golang:1.25 go test -race -run TestEstadoInicialMongo -v ./internal/ingest/
```