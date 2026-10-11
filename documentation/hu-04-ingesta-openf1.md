# HU-04 — Ingesta de datos de carrera desde OpenF1

## Contexto

El motor de apuestas necesita datos reales de una carrera para que los
jugadores puedan apostar sobre lo que pasa en la pista. Esta HU cubre
la lectura de esos datos desde una API externa, su normalización y su
persistencia en MongoDB.

## Requisitos cubiertos

| ID    | Descripción                                                                                                                | Componente        |
| ----- | -------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| RF-05 | El motor lee datos de una API externa (en vivo o de una carrera pasada reproducida), los normaliza y los guarda en MongoDB | Motor de apuestas |
| RF-06 | El front muestra las posiciones al entrar y las mantiene actualizadas por WebSocket, sin botones ni recargas                 | Front, motor      |
| HU-04 | Como jugador, quiero que la app tenga datos reales de una carrera, para que las apuestas se basen en lo que pasa en la pista | Motor de apuestas |

## Decisión: OpenF1, no FastF1

Se evaluaron dos fuentes de datos:

| Fuente  | Pros                                                    | Contras                                                                                 |
| ------- | ------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| OpenF1  | REST + JSON, sin dependencias, datos históricos gratuitos | Cobertura limitada a sesiones de F1 modernas                                            |
| FastF1  | Datos más ricos (vueltas, neumáticos, telemetría)         | Librería Python, exige un proceso adicional con su propio componente y MongoDB compartida |

**Decisión:** usar OpenF1 para el Prototipo 1. FastF1 queda como evolución
futura si se necesitan más datos para generar mercados automáticos.

## Flujo de la ingesta

```
┌─────────────┐   1. POST /ingest/openf1    ┌──────────────────┐
│ Admin       │ ───────────────────────────▶│ Motor (Go)       │
└─────────────┘                              └────────┬─────────┘
                                                      │
                                            2. GET /position?session_key=X
                                            3. GET /drivers?session_key=X
                                                      │
                                                      ▼
                                              ┌───────────────┐
                                              │ OpenF1 API    │
                                              └───────┬───────┘
                                                      │ JSON
                                                      ▼
                                              ┌───────────────┐
                                              │ normalize.go  │
                                              └───────┬───────┘
                                                      │ EventoCarrera
                                                      ▼
                                              ┌────────────────┐
                                              │ MongoDB        │
                                              │ eventos_carrera│
                                              └────────────────┘
```

### Paso 1: el admin dispara la ingesta

```
POST /ingest/openf1
Cookie: session=<JWT con rol admin>
Content-Type: application/json

{ "carrera_id": "2024_monza", "session_key": 9590 }
```

### Pasos 2 y 3: el motor consulta OpenF1

- `GET https://api.openf1.org/v1/position?session_key=9590`
- `GET https://api.openf1.org/v1/drivers?session_key=9590`

Ambas devuelven JSON.

### Paso 4: normalización

Cada posición cruda de OpenF1 se transforma en un `EventoCarrera`:

**Entrada (OpenF1):**

```json
{
  "date": "2024-09-01T12:07:46.509Z",
  "driver_number": 4,
  "position": 1,
  "session_key": 9590
}
```

**Salida (MongoDB):**

```json
{
  "carrera_id": "2024_monza",
  "timestamp": "2024-09-01T12:07:46.509Z",
  "tipo": "posicion",
  "datos": { "driver_number": 4, "position": 1, "session_key": 9590 },
  "leido_en": "2026-10-11T05:46:35.865Z"
}
```

La lista de pilotos se guarda como un evento `tipo: "vuelta"` con
`datos.tipo_vuelta: "lista_pilotos"` y un array de pilotos. Así el front
puede cruzar `driver_number` con nombres sin llamar a OpenF1.

### Paso 5: persistencia

Se hace un único `InsertMany` con todos los documentos. Si algo falla,
la operación se aborta y no deja datos a medias.

## Replay por WebSocket

Para la demo, el sistema no depende de que OpenF1 esté disponible. El
replay lee de `eventos_carrera` y los difunde por WebSocket:

```
POST /ingest/replay
Cookie: session=<JWT con rol admin>

{ "carrera_id": "2024_monza", "factor": 10 }
```

- `factor`: divisor del intervalo original. `1` = tiempo real, `10` = 10×.
- Cada evento se emite con `type: "evento_carrera"` y el `EventoCarrera`
  completo en `payload`.

Esto también sirve para las **pruebas de carga** (HU-09): con factor alto
se puede simular una carrera completa en segundos.

## Contrato de MongoDB

Colección: `eventos_carrera`. Validador en `db/mongo/init/01-init-collections.js`.

| Campo        | Tipo   | Requerido | Descripción                                |
| ------------ | ------ | --------- | ------------------------------------------ |
| `carrera_id` | string | sí        | Referencia a `carreras.carrera_id`         |
| `timestamp`  | date   | sí        | Momento del evento en la carrera           |
| `tipo`       | string | sí        | Uno del enum del validador                 |
| `datos`      | object | sí        | Payload variable según el tipo             |
| `leido_en`   | date   | no        | Cuándo lo leyó el motor del sistema        |

Índices:

- `{ carrera_id: 1, timestamp: 1 }` — para el replay en orden.
- `{ carrera_id: 1, tipo: 1, timestamp: 1 }` — para filtrar por tipo.

## Seguridad

- La ruta exige rol `admin` (middleware `auth.ExigirRol`).
- El JWT se manda en cookie `session` (HttpOnly), no en header.
- El motor valida la firma por su cuenta con el secreto compartido; no
  consulta a Pagos en cada petición.
- El `sub` del JWT debe ser un UUID válido.

## Pruebas

Tests unitarios: `services/motor/internal/ingest/ingest_test.go`.

- `TestNormalizarPosicion`: verifica la transformación de posición cruda
  a `EventoCarrera`.
- `TestOpenF1ClientPosiciones`: levanta un `httptest.Server` y verifica
  que el cliente parsea bien la respuesta de OpenF1.

Correr:

```bash
cd services/motor
go test ./internal/ingest/...
```

## Resultado de la prueba manual

Ejecutada el 2026-10-11 contra Monza 2024 Race (`session_key=9590`):

```json
{"insertados":483}
```

Verificado en MongoDB:

```
> db.eventos_carrera.countDocuments({carrera_id: '2024_monza'})
493   // 483 de OpenF1 + 10 del seed de prueba
```

Documento de muestra:

```json
{
  _id: ObjectId('6acb22bbcfde3825efac8586'),
  carrera_id: '2024_monza',
  timestamp: ISODate('2024-09-01T12:07:46.509Z'),
  tipo: 'posicion',
  datos: { session_key: 9590, driver_number: 4, position: 1 },
  leido_en: ISODate('2026-10-11T05:46:35.865Z')
}
```