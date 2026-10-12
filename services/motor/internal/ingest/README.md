# Ingesta de datos de carrera (HU-04)

Este paquete implementa la **HU-04**: leer datos de una API externa (OpenF1),
normalizarlos y guardarlos en MongoDB para que el front los consuma y el
motor pueda resolver mercados con ellos.

## Arquitectura

```
OpenF1 API  ──HTTP──▶  ingest.Servicio  ──InsertMany──▶  MongoDB (eventos_carrera)
                            │
                            │ Replay
                            ▼
                       WebSocket (hub)  ──▶  Front
```

Cada evento guardado en `eventos_carrera` tiene la forma:

```json
{
  "carrera_id": "2024_monza",
  "timestamp": "2024-09-01T12:07:46.509Z",
  "tipo": "posicion",
  "datos": { "driver_number": 4, "position": 1, "session_key": 9590 },
  "leido_en": "2026-10-11T05:46:35.865Z",
  "emitido_en": "2026-10-12T15:00:41.207Z"
}
```

Las tres fechas significan cosas distintas:

| Campo        | Qué es                                                        | Quién lo escribe |
| ------------ | ------------------------------------------------------------- | ---------------- |
| `timestamp`  | Momento del evento en la carrera (`date` de OpenF1). Histórico | Ingesta          |
| `leido_en`   | Hora real en que se descargó de OpenF1                         | Ingesta          |
| `emitido_en` | Hora real en que la reproducción lo emitió por WebSocket       | Replay           |

`emitido_en` no existe hasta que el evento se reproduce, y se sobrescribe si
se vuelve a reproducir. Es la fecha que usa la liquidación (HU-07): ver
"Replay por WebSocket" más abajo.

El campo `tipo` debe ser uno de los aceptados por el validador de la
colección (ver `db/mongo/init/01-init-collections.js`):

- `posicion`
- `vuelta`
- `bandera`
- `clima`
- `sector`
- `pit_stop`

## Endpoints

| Método | Ruta             | Rol   | Descripción                                                       |
| ------ | ---------------- | ----- | ----------------------------------------------------------------- |
| POST   | `/ingest/openf1` | admin | Lee posiciones y pilotos de OpenF1 y los guarda en MongoDB        |
| POST   | `/ingest/replay` | admin | Reproduce por WebSocket los eventos ya guardados de una carrera   |

Ambas rutas exigen el rol `admin`. El JWT se manda en la cookie `session`,
no en el header `Authorization` (ver `internal/auth/auth.go`).

## Estructura del paquete

| Archivo          | Responsabilidad                                                       |
| ---------------- | --------------------------------------------------------------------- |
| `openf1.go`      | Cliente HTTP a la API de OpenF1 (`/position`, `/drivers`, `/sessions`) |
| `normalize.go`   | Tipos crudos de OpenF1 + normalización al esquema de `eventos_carrera` |
| `servicio.go`    | Orquestador: OpenF1 → normalización → `InsertMany` en MongoDB          |
| `replay.go`      | Lee eventos de MongoDB y los emite por un canal, respetando intervalos; marca `emitido_en`, escribe el latido y elige el offset |
| `handler.go`     | Registro de rutas y handlers HTTP                                      |
| `ingest_test.go` | Tests unitarios del cliente y la normalización                        |

## Cómo probar

### 1. Encuentra un `session_key` de OpenF1

```bash
curl "https://api.openf1.org/v1/sessions?year=2024&location=Monza&session_name=Race"
```

Ejemplo para Monza 2024 Race: `session_key=9590`.

### 2. Genera un JWT de admin

Como el login de Pagos (HU-02) todavía no está implementado, se firma un JWT
a mano con el `JWT_SECRET` compartido, usando el contenedor de Pagos (que ya
tiene Python y PyJWT):

```powershell
docker exec -it arqui_pagos python -c "import os, jwt, datetime; secret = os.environ['JWT_SECRET']; token = jwt.encode({'sub': '00000000-0000-0000-0000-000000000001', 'rol': 'admin', 'exp': datetime.datetime.now(datetime.UTC) + datetime.timedelta(hours=1)}, secret, algorithm='HS256'); print(token)"
```

> El `sub` **debe ser un UUID válido** (el validador lo exige) y el `rol`
> debe ser `admin`. El JWT se manda al motor en la cookie `session`.

### 3. Dispara la ingesta

**En PowerShell**, ojo con los escapes: pasar el body en archivo evita que
PowerShell rompa las comillas.

```powershell
$TOKEN = "eyJ..."   # pega el token de arriba

Set-Content -Path body.json -Value '{"carrera_id":"2024_monza","session_key":9590}'

curl.exe -X POST http://localhost:8080/ingest/openf1 `
  -H "Cookie: session=$TOKEN" `
  -H "Content-Type: application/json" `
  --data-binary "@body.json"

Remove-Item body.json
```

Respuesta esperada:

```json
{"insertados":483}
```

### 4. Verifica en MongoDB

```powershell
docker exec -it arqui_mongo mongosh "mongodb://arqui:arqui_pass@localhost:27017/apuestas_db?authSource=admin" `
  --eval "print(db.eventos_carrera.countDocuments({carrera_id:'2024_monza'}))"
```

Debe devolver un número > 0 (por ejemplo `483`, más los 10 del seed si
`db/mongo/init/02-seed-data.js` también corrió).

Y un documento de muestra:

```powershell
docker exec -it arqui_mongo mongosh "mongodb://arqui:arqui_pass@localhost:27017/apuestas_db?authSource=admin" `
  --eval "printjson(db.eventos_carrera.findOne({carrera_id:'2024_monza', tipo:'posicion'}))"
```

## Replay por WebSocket

La carrera es pasada: se descargó una vez y el replay la "vuelve a vivir".
Se dispara así:

```bash
curl -X POST http://localhost:8080/ingest/replay \
  -H "Cookie: session=$TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"carrera_id":"2024_monza","factor":1}'
```

Respuesta (la reproducción sigue en segundo plano, máximo 30 min):

```json
{"estado":"reproduciendo","carrera_id":"2024_monza","factor":1,"offset_segundos":1834.2,"desde":"2024-09-01T13:33:12Z"}
```

El motor lee de `eventos_carrera` en orden cronológico y difunde cada evento
por WebSocket con `type: "evento_carrera"`. Campos del cuerpo:

- `factor`: velocidad. `1` es tiempo real, `10` es 10× más rápido. Para los
  mercados Sí/No usa `1`: la ventana de observación son 60 s reales, que con
  `factor: 10` serían 10 minutos de carrera (casi siempre "Sí").
- `offset_segundos` (opcional): desde qué segundo de la carrera reproducir,
  contado desde la primera posición. Si no viene, se elige **al azar** entre
  10 min después del inicio (pasadas las primeras ~6 vueltas, para no
  empezar con la parrilla de salida) y 15 min antes del final. Si la carrera
  es más corta que eso (p. ej. el seed), se reproduce desde el principio.
  Responde `400` si el offset está fuera de la carrera y `404` si la carrera
  no tiene posiciones.

Para que la liquidación (HU-07) pueda resolver mercados sobre una carrera
pasada, el replay deja dos rastros con el reloj **real** del servidor:

1. `emitido_en` en cada evento, justo antes de difundirlo. Si MongoDB falla
   al marcarlo, el replay se corta (un evento emitido sin marca sería
   invisible para la liquidación).
2. Un **latido** en la colección `reproducciones` (un documento por carrera,
   `_id = carrera_id`): `iniciada_en` al arrancar, `latido_en` cada 5 s y
   `terminada_en` al acabar. Con eso la liquidación sabe si el replay estuvo
   corriendo durante toda la ventana de un mercado y puede dar "No" cuando no
   hubo cambios de posición.

El evento de lista de pilotos no se reproduce: su `timestamp` es la hora de
la ingesta, no un momento de la carrera, y dejaría el replay esperando al
final.

Verifica el latido en MongoDB:

```powershell
docker exec -it arqui_mongo mongosh "mongodb://arqui:arqui_pass@localhost:27017/apuestas_db?authSource=admin" `
  --eval "printjson(db.reproducciones.findOne({_id:'2024_monza'}))"
```

## Fuente de datos: OpenF1

Se usa **OpenF1** (https://openf1.org) porque:

- Devuelve JSON, que Go consume directamente.
- Ofrece datos históricos gratuitos con licencia no comercial.
- No requiere un proceso Python adicional (a diferencia de FastF1).

Para la demo, se descarga la sesión una sola vez y se guarda en MongoDB.
Así el sistema no depende de la disponibilidad de la API durante la
presentación.

## Requisitos cubiertos

- **HU-04**: "Como jugador, quiero que la app tenga datos reales de una
  carrera, para que las apuestas se basen en lo que pasa en la pista."
- **RF-05**: el motor lee datos de una API externa, los normaliza y los
  guarda en MongoDB.
- **RF-06**: el front los recibe por WebSocket (implementado en el hub;
  el endpoint de replay es la pieza que faltaba).