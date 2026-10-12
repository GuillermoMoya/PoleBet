# HU-11 — Abrir y cerrar un mercado (administrador)

## Qué cubre

| ID     | Requisito                                                                                      |
| ------ | ---------------------------------------------------------------------------------------------- |
| RF-07  | El administrador crea un mercado con pregunta y opciones y lo abre; los jugadores lo reciben por WebSocket con la cuenta regresiva |
| RF-09  | Al terminar la ventana de apuestas (30 s, configurable) el mercado se cierra y empieza 1 minuto de observación |
| RNF-05 | El reloj del servidor decide el cierre; el front solo muestra la cuenta regresiva               |

Componentes: motor (Go, MongoDB) y front (React).

## Cómo funciona

```
Admin ──POST /mercados──▶ Motor ──guarda──▶ MongoDB (mercados)
                            │
                            ├─ WS mercado_abierto ──▶ todos los jugadores
                            │
              cierra_en ────┼─ estado = cerrado
                            ├─ WS mercado_cerrado ──▶ todos los jugadores
                            │
          observa_hasta ────┴─ Liquidador.Liquidar()  (HU-07)
```

1. El admin envía la pregunta y las opciones. El motor guarda el mercado con
   `abre_en`, `cierra_en = abre_en + BETTING_WINDOW_SECONDS` y
   `observa_hasta = cierra_en + OBSERVATION_WINDOW_SECONDS`, y lo difunde.
2. Un temporizador del servidor lo pasa a `cerrado` en `cierra_en`. El cambio
   es una actualización condicional (`estado: abierto → cerrado`), así que no
   pasa nada si se intenta cerrar dos veces.
3. Al terminar la observación se llama al `Liquidador`, que lo implementa HU-07.
   Mientras HU-07 no exista, el motor solo lo deja en el log.

Las apuestas no dependen de que el temporizador llegue a tiempo: HU-06 también
compara `cierra_en` con el reloj del servidor y rechaza con «Mercado cerrado».

## API del motor

| Ruta                    | Quién            | Respuesta                                            |
| ----------------------- | ---------------- | ---------------------------------------------------- |
| `POST /mercados`        | solo admin       | `201` mercado abierto · `400` validación · `404` carrera inexistente · `409` ya hay uno abierto · `422` no hay carreras cargadas |
| `GET /mercados/actual`  | cualquier sesión | `200` mercado más reciente o `null`                  |

```json
{ "pregunta": "¿Quién irá primero al terminar el minuto?", "opciones": ["VER", "NOR", "LEC"] }
```

- `carrera_id` es opcional; sin él se usa la última carrera cargada (HU-04).
- La pregunta puede tener de 1 a 200 caracteres. Se permiten de 2 a 10
  opciones, sin repetir, de máximo 100 caracteres cada una (es el largo de
  `bets.option` en Pagos).
- Sin sesión se responde 401 y con rol jugador 403 (`auth.ExigirRol`).

`mercado_abierto`, `mercado_cerrado` y `GET /mercados/actual` envían el mismo
objeto: `id`, `carrera_id`, `pregunta`, `opciones`, `estado`, `abre_en`,
`cierra_en`, `observa_hasta` y `ahora`. `ahora` es la hora del servidor: el
front la usa para calcular el desfase de su reloj y mostrar la misma cuenta
regresiva que el servidor.

## Decisiones

- **Un solo mercado abierto a la vez.** Lo garantiza el servicio con un mutex
  y MongoDB con un índice único parcial (`uq_un_mercado_abierto`, sobre
  `estado` cuando vale `abierto`) que el motor crea al arrancar.
- **Recuperación tras reinicio.** Al arrancar, el motor cierra los mercados
  que vencieron mientras estaba apagado y vuelve a programar los demás. Por
  eso la liquidación de HU-07 debe ser idempotente: puede volver a llamarse
  para un mercado cerrado que no se liquidó.
- **El admin escribe la pregunta y las opciones** (pregunta abierta n.º 3 del
  documento del prototipo).

## Front

- `/admin`: formulario para abrir un mercado (pregunta y de 2 a 10 opciones,
  validados con las mismas reglas del motor) y el mercado actual con su estado
  y su cuenta regresiva.
- `/carrera`: el mercado aparece y cambia solo, por WebSocket. Mientras está
  abierto se ve el formulario de apuesta de HU-06; cuando cierra, el
  formulario desaparece. Se muestra el ciclo
  **Abierto → Cerrado (en observación) → Liquidado**.
- Código en `services/front/src/mercados/` (`useMercado`, `EstadoMercado` y
  `MercadoJugador`). Se eliminó la demo simulada `DemoApuesta.jsx`.

## Archivos principales

- `services/motor/internal/mercados/`: `servicio.go` (ciclo y validación),
  `mongo.go`, `handler.go` y `mercados_test.go`.
- `services/motor/main.go`: `registrarMercados`.
- `services/front/src/mercados/` y `services/front/src/paginas/Admin.tsx`.

## Verificación

- Motor: `go vet`, `go build` y `go test ./...` pasan, con 17 pruebas nuevas
  de validación, ciclo completo, concurrencia, recuperación y handler.
- Front: `tsc`, `npm test` (99 pruebas, 28 nuevas) y `npm run build` pasan.
- Prueba manual con Docker Compose:
  - el endpoint responde 401 sin sesión, 403 a un jugador, 400 a un mercado
    inválido, 201 al abrir y 409 a un segundo mercado;
  - un jugador conectado por WebSocket recibió `mercado_abierto` al instante y
    `mercado_cerrado` 30 s después;
  - `POST /apuestas` sobre el mercado cerrado respondió 409 «Mercado cerrado».

## Pendiente (otras HU)

- **HU-07:** implementar `mercados.Liquidador`, asignarlo en
  `registrarMercados` y difundir `mercado_liquidado`. El front ya lo escucha,
  con el mercado completo o solo con su `id`.
