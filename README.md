# Prototipo 1 — ArquiSoft

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
