# Motor de apuestas (Go) - Prototipo 1

Plataforma de apuestas en vivo sobre carreras de F1 con **tokens simulados**
(sin dinero real). Este repositorio contiene el **Prototipo 1**: un corte
vertical que va de *registro → compra de tokens → datos de una API en el front
→ apuesta en un mercado abierto por el admin → liquidación → saldo actualizado*.

## Arquitectura

Tres componentes lógicos, cada uno dueño de su base de datos y comunicados por
contrato (REST + WebSocket), nunca por BD compartida:

| Componente          | Lenguaje / Stack      | Base de datos | Conectores        |
| ------------------- | --------------------- | ------------- | ----------------- |
| **Front**           | React + Vite (Nginx)  | —             | REST + WebSocket  |
| **Pagos y usuarios**| Python / FastAPI      | PostgreSQL    | REST              |
| **Motor de apuestas**| Go                   | MongoDB       | REST + WebSocket  |

El motor nunca escribe en PostgreSQL: cuando necesita mover saldo o puntos se
lo pide a Pagos por REST con una clave de idempotencia.

```
services/
├── front/   # React + Vite, servido por Nginx
├── pagos/   # FastAPI + SQLAlchemy (PostgreSQL)
└── motor/   # Go + gorilla/websocket + mongo-driver (MongoDB)
```

## Requisitos

- [Docker](https://docs.docker.com/get-docker/) con Docker Compose v2
  (`docker compose`, incluido en Docker Desktop y en el plugin moderno).

Nada más: no hace falta instalar Go, Python ni Node en la máquina.

## Levantar el sistema

Un solo comando desde la raíz del repositorio:

```bash
cp .env.example .env     # solo la primera vez
# Edita .env, define ADMIN_EMAIL y ADMIN_PASSWORD, y genera un
# INTERNAL_API_TOKEN aleatorio (el mismo valor se usa en Motor y Pagos).
docker compose up --build
```

Cuando todos los servicios estén arriba, abre el front en el navegador:

- **Front:** http://localhost:5173
- API Pagos (REST): http://localhost:8000/health
- API Motor (REST): http://localhost:8080/health
- WebSocket Motor: ws://localhost:8080/ws

La pantalla http://localhost:5173/estado muestra el estado de los dos backends
y de la conexión WebSocket, lo que confirma que los tres componentes se
comunican. Las rutas y el desarrollo del front están en `services/front/README.md`.

Para detener y limpiar:

```bash
docker compose down          # detiene los contenedores
docker compose down -v       # además borra los volúmenes de datos (BD)
```

## Configuración

Todo se configura en `.env` (copiado de `.env.example`). Variables clave:

| Variable                     | Descripción                                   |
| ---------------------------- | --------------------------------------------- |
| `FRONT_PORT` / `PAGOS_PORT` / `MOTOR_PORT` | Puertos expuestos en el host    |
| `JWT_SECRET`                 | Secreto JWT compartido entre pagos y motor    |
| `COP_PER_TOKEN`              | Tasa fija COP → token (RF-03)                 |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | Credenciales del admin (RF-13 / HU-12)    |
| `BETTING_WINDOW_SECONDS`     | Duración de la ventana de apuestas (RF-07/09) |
| `OBSERVATION_WINDOW_SECONDS` | Ventana de observación antes de liquidar      |
| `INTERNAL_API_TOKEN`         | Token compartido Motor→Pagos para operaciones internas |
| `MONGO_USER` / `MONGO_PASSWORD` / `MONGO_DB` | Credenciales de MongoDB       |

## Liquidación HU-07

Hay un solo tipo de mercado, Sí/No: **«¿Habrá algún cambio de posición durante
la ventana de observación?»**. Gana "Sí" si la reproducción de la carrera
(`POST /ingest/replay`) emite algún evento de posición durante la ventana, y
"No" si no emite ninguno mientras está corriendo. Para la demo:

1. Ingesta la carrera una vez (`POST /ingest/openf1`).
2. Lanza `POST /ingest/replay` con `"factor": 1` y déjala corriendo; empieza
   en un punto aleatorio de la carrera, después de las primeras vueltas.
3. Abre el mercado desde `/admin` y apuesta desde `/carrera`; 30 s + 60 s
   después queda liquidado.

Usa siempre `carrera_id` **`2024_monza`**: el mercado se abre sobre la última
carrera de la colección `carreras`, que hoy solo llena el seed con ese id. Con
otro id, el mercado no ve los eventos ni el latido y queda pendiente.

Comandos exactos (PowerShell, con el stack levantado):

```powershell
# 1. JWT de admin (cookie session para el motor)
$TOKEN = docker exec arqui_pagos python -c "import os, jwt, datetime; secret = os.environ['JWT_SECRET']; token = jwt.encode({'sub': '00000000-0000-0000-0000-000000000001', 'rol': 'admin', 'exp': datetime.datetime.now(datetime.UTC) + datetime.timedelta(hours=1)}, secret, algorithm='HS256'); print(token)"

# 2. Ingesta de Monza 2024 (Race, session_key 9590) desde OpenF1, una sola vez
Set-Content -Path body.json -Value '{"carrera_id":"2024_monza","session_key":9590}'
curl.exe -X POST http://localhost:8080/ingest/openf1 `
  -H "Cookie: session=$TOKEN" `
  -H "Content-Type: application/json" `
  --data-binary "@body.json"

# 3. Reproduccion en tiempo real desde un punto aleatorio (dura hasta 30 min)
Set-Content -Path body.json -Value '{"carrera_id":"2024_monza","factor":1}'
curl.exe -X POST http://localhost:8080/ingest/replay `
  -H "Cookie: session=$TOKEN" `
  -H "Content-Type: application/json" `
  --data-binary "@body.json"
Remove-Item body.json
```

La respuesta de la reproducción incluye `offset_segundos` (dónde empezó).
Para repetir exactamente la misma prueba, agrega `"offset_segundos": 1800`
al cuerpo del paso 3.

Requiere `PAGOS_MOCK=false`, `INTERNAL_API_TOKEN` idéntico en ambos servicios
y apuestas debitadas en Pagos. Si cambias `db/mongo/init`, recrea el volumen
de MongoDB (`docker compose down -v`). La regla completa, el contrato con
Pagos, las decisiones y los límites están en
[`documentation/hu-07-liquidacion-apuestas.md`](documentation/hu-07-liquidacion-apuestas.md).

## Ingesta de datos de carrera (HU-04)

El paquete `internal/ingest` del motor trae datos de **OpenF1** y los guarda
normalizados en MongoDB. Ver
[`services/motor/internal/ingest/README.md`](services/motor/internal/ingest/README.md)
para el detalle completo.

Resumen rápido:

1. Encuentra un `session_key` de OpenF1:

   ```bash
   curl "https://api.openf1.org/v1/sessions?year=2024&location=Monza&session_name=Race"
   ```

   Para Monza 2024 Race: `session_key=9590`.

2. Genera un JWT de admin (temporal, hasta que HU-02 esté lista):

   ```powershell
   docker exec -it arqui_pagos python -c "import os, jwt, datetime; secret = os.environ['JWT_SECRET']; token = jwt.encode({'sub': '00000000-0000-0000-0000-000000000001', 'rol': 'admin', 'exp': datetime.datetime.now(datetime.UTC) + datetime.timedelta(hours=1)}, secret, algorithm='HS256'); print(token)"
   ```

3. Dispara la ingesta (ojo con los escapes de PowerShell):

   ```powershell
   Set-Content -Path body.json -Value '{"carrera_id":"2024_monza","session_key":9590}'

   curl.exe -X POST http://localhost:8080/ingest/openf1 `
     -H "Cookie: session=$TOKEN" `
     -H "Content-Type: application/json" `
     --data-binary "@body.json"

   Remove-Item body.json
   ```

4. Verifica en MongoDB:

   ```powershell
   docker exec -it arqui_mongo mongosh "mongodb://arqui:arqui_pass@localhost:27017/apuestas_db?authSource=admin" `
     --eval "print(db.eventos_carrera.countDocuments({carrera_id:'2024_monza'}))"
   ```

> **Nota:** el JWT se manda al motor como cookie `session`, no como header
> `Authorization`. El motor solo valida la firma y el rol; no consulta a
> Pagos en cada petición.

## Datos de prueba

`db/mongo/init/02-seed-data.js` carga la carrera `2024_monza` y unos eventos
solo al primer arranque de un volumen Mongo vacío. No crea mercados: los abre
el admin desde `/admin`. Los volúmenes existentes no se recrean ni migran
automáticamente: si un volumen viejo tiene mercados sin `tipo:
"cambio_posicion"` (p. ej. el mercado que sembraban versiones anteriores),
HU-07 no los liquidará; recrea el volumen o bórralos.
No borres volúmenes que contengan datos que quieras conservar.