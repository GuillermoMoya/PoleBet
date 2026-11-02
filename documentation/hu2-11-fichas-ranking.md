# HU2-11 — Fichas y ranking del torneo

## Resumen

En el torneo multijugador cada inscrito juega con **fichas** (distintas de los
tokens: se reparten al inscribirse, solo valen dentro del evento y no se
compran). Esta historia hace que, al resolverse un mercado, las apuestas de
torneo se paguen en fichas y que todos vean el ranking actualizado en vivo, con
una barra que marca cuánto lleva apostado cada quien frente al mínimo que lo
hace elegible para el reparto del botín (HU2-12).

Las fichas **viven en Redis**, del que el Motor es el único dueño (RNF-18). En
PostgreSQL de Pagos solo queda el resultado final (`chips_final`,
`chips_wagered`), que escribe el cierre del evento (HU2-12).

## Qué cubre

| ID | Requisito |
| --- | --- |
| RF-15 | Una apuesta de torneo ganadora recibe `fichas × multiplicador`; una perdedora, nada. El saldo en tokens no cambia |
| RF-18 | Los inscritos ven fichas, puesto, botín y la barra de avance hacia el mínimo apostado, en vivo |
| RF-17 (parte torneo) | Al liquidarse, el jugador recibe por WebSocket sus fichas ganadas o perdidas y su nuevo puesto |
| RNF-12 | Idempotencia: repetir una liquidación o apostar desde dos navegadores no duplica fichas. En Redis las fichas se escriben, no se suman |
| RNF-13 | El ranking cambia en pantalla en menos de 1 s |

Componentes: Motor (Go), Redis, Front (tipos del contrato; la UI va como paso
siguiente de la HU).

## Dónde encaja (fronteras con el equipo)

El torneo lo construyen varias personas; esta HU es dueña del **almacén de
fichas y el ranking**. Las fronteras acordadas (reparto del Prototipo 2 y PR #26
de Michael):

```
HU2-06 (Michael, Pagos)        HU2-09 (Michael, Motor)        HU2-11 (esta HU)
  largar el torneo               apostar en modo torneo         liquidar en fichas
  POST …/start → players    →    ReservarApuesta (descuenta) →  Liquidar (acredita)
                                                                 + difundir ranking
                                       HU2-12 (reparto): lee el estado final y lo
                                       lleva a event_entries, luego limpia Redis
```

- **HU2-06 (largada):** `POST /internal/events/{id}/start` de Pagos devuelve la
  lista de inscritos (`players`) e `initial_chips`. Con eso el Motor inicializa
  las fichas (`Almacen.InicializarTorneo`). La doc de Michael lo dice explícito:
  «las fichas viven en Redis; `players` de la largada dice a quién crearle
  `initial_chips`».
- **HU2-09 (apostar):** al confirmar una apuesta de torneo, el flujo de apuestas
  llama a `Almacen.ReservarApuesta` para descontar las fichas antes de registrar
  la apuesta en Pagos. Esta HU **expone** esa operación; quien implemente HU2-09
  la consume.
- **HU2-12 (reparto):** lee el estado final de las fichas y lo persiste en
  `event_entries`, y al terminar llama a `Almacen.Limpiar` (RF-16: «Redis se
  limpia»).

Nota: el disparo de la largada (detectar el tiempo de carrera y llamar al
`start`) pertenece a HU2-07 (Tomás). Mientras no exista, `InicializarTorneo` se
puede invocar manualmente para probar; no bloquea esta HU.

## Cómo funciona

El worker de liquidación de HU-07 ya consulta los mercados cerrados cuya
observación terminó y resuelve la opción ganadora. Esta HU lo extiende: por cada
apuesta del mercado, decide según el **modo** del contexto guardado en MongoDB:

```
            ┌─ modo = individual ─▶ Pagos.Liquidar (tokens)      [HU-07, sin cambios]
apuesta ────┤
            └─ modo = torneo ─────▶ fichas.Almacen.Liquidar (Redis) + resultado + ranking
```

- **Individual:** exactamente igual que antes (puntos a Pagos). No se tocó esa
  ruta salvo para dejarla pasar.
- **Torneo:** se paga en fichas, se marca el contexto como liquidado en MongoDB
  (con `puntos = 0`, el torneo no usa puntos) y se envía `resultado_apuesta` con
  fichas y puesto. Tras liquidar todas las apuestas de torneo del mercado, se
  difunde el ranking una vez.

### Idempotencia (RNF-12), la decisión central

Las fichas **nunca se suman** con un incremento ciego. El esquema es:

1. Cada apuesta escribe su **reserva** (lo apostado) una sola vez.
2. Cada liquidación escribe su **resultado** (ganó/perdió y fichas pagadas) una
   sola vez.
3. El saldo del jugador se **recalcula** siempre:
   `saldo = inicial − Σ reservas + Σ pagos de apuestas resueltas`.

Así, repetir una liquidación, reintentar una apuesta o reprocesar un mercado tras
un reinicio del Motor deja las fichas idénticas. Es el equivalente en Redis de la
idempotencia por `idempotency_key` que Pagos usa para los tokens.

### Ranking y barra de avance (RF-18, RNF-13)

El ranking ordena a los inscritos por fichas (mayor a menor). Cada grupo
empatado ocupa un solo puesto y el siguiente grupo toma el puesto que sigue
(1,1,1,2), como dice el enunciado: «si tres empatan en el 1.º, … el siguiente
jugador es el 2.º». Es la misma regla con la que reparte el botín HU2-12. La barra de avance compara las fichas apostadas con el mínimo
(`EVENT_MIN_WAGER_PCT` de las fichas iniciales, 50 % por defecto, redondeado
hacia arriba): al alcanzarlo el jugador es `elegible` para el reparto. Se
difunde por WebSocket con el mismo patrón que la tabla de posiciones de HU-05
(foto completa, `emitido_en` para medir la latencia).

## Claves de Redis

El Motor es el único que escribe estas claves. Prefijo por evento para poder
limpiarlas todas al cerrar (HU2-12).

| Clave | Tipo | Contenido |
| --- | --- | --- |
| `torneo:{evento}:inicial` | String | fichas iniciales; su existencia marca «torneo activo» |
| `torneo:{evento}:inscritos` | Set | `usuario_id` de cada inscrito |
| `torneo:{evento}:apuestas:{usuario}` | Set | `apuesta_id` de ese usuario |
| `torneo:{evento}:reserva:{apuesta}` | Hash | usuario, monto, opción, multiplicador |
| `torneo:{evento}:resultado:{apuesta}` | Hash | ganó (0/1), fichas pagadas |
| `torneo:{evento}:fichas` | Hash | `usuario → saldo` (cache del ranking, recalculada) |
| `torneo:{evento}:apostado` | Hash | `usuario → apostado` (cache del ranking, recalculada) |

Las dos últimas son **cache derivada**: se reescriben en cada recálculo a partir
de reservas y resultados, nunca con `INCRBY`. Las claves expiran a las 24 h por
si HU2-12 no alcanza a limpiarlas (una carrera dura minutos).

> Convención a compartir con Michael (HU2-09): si su flujo de apuesta escribe o
> lee fichas, debe hacerlo solo a través de `internal/fichas` (no tocar estas
> claves directamente), para que la idempotencia por recálculo se mantenga. Si
> Michael ya definió otro esquema de claves, se reconcilia cambiando únicamente
> `internal/fichas/redis.go` (la lógica y el contrato Go no cambian).

## API interna (Go) que expone esta HU

En `services/motor/internal/fichas`:

| Operación | Para quién | Qué hace |
| --- | --- | --- |
| `Almacen.InicializarTorneo(evento, jugadores, fichasIniciales)` | largada (HU2-06/07) | da fichas iniciales a cada inscrito; idempotente |
| `Almacen.ReservarApuesta(evento, Reserva)` | apostar torneo (HU2-09) | descuenta fichas; rechaza sin saldo o con contenido distinto |
| `Almacen.Liquidar(evento, apuesta, ganó)` | worker de liquidación | acredita `monto × multiplicador` si ganó; idempotente |
| `Almacen.LiquidarYUbicar(…)` | worker (vía adaptador) | lo anterior + fichas totales y puesto, para `resultado_apuesta` |
| `Almacen.Limpiar(evento)` | cierre (HU2-12) | borra todas las claves del evento |
| `Difusor.Difundir(evento)` | worker | arma y difunde el ranking por WebSocket |

El worker (paquete `liquidacion`) usa una interfaz `LiquidadorTorneo` que el
adaptador de `main.go` cumple con el `Almacen`. Así `fichas` no importa
`liquidacion` (sin ciclo) y el liquidador de torneo es **opcional**: sin Redis el
motor funciona igual.

## Contrato WebSocket

- Mensaje nuevo `ranking_torneo` (difusión a todos). Payload en el README del
  Motor y en `services/front/src/api/tipos.ts` (`RankingTorneo`).
- `resultado_apuesta` (HU-08) gana campos opcionales para torneo: `modo`,
  `fichas_ganadas`, `fichas_total`, `puesto`. En individual no aparecen y el
  contrato de HU-08 (puntos/puntaje) no cambia.

## Decisiones

1. **Dueño del almacén de fichas = esta HU.** El reparto da las fichas/Redis al
   Motor. En vez de esperar a HU2-09, esta HU entrega el componente completo y
   expone la API que HU2-09 consumirá. Desacopla el trabajo y respeta RNF-18.
2. **Recalcular en vez de sumar.** Es la forma de cumplir RNF-12 sin
   transacciones distribuidas: escribir el resultado por apuesta (idempotente) y
   derivar el total. Sobrevive a reintentos y reinicios.
3. **Modo en el contexto de la apuesta (cambio aditivo).** El contexto en
   MongoDB gana `modo`, `multiplicador` y `evento_id`, opcionales y con valor
   por defecto individual. Un contexto viejo se sigue liquidando como antes.
   Esto toca código que también usa HU2-06/09 (Michael); se hizo aditivo a
   propósito para que un merge no entre en conflicto. Si Michael guarda el modo
   con otro nombre, se ajusta solo el decodificador de `liquidacion/mongo.go`.
4. **Liquidador de torneo opcional en el worker.** Se inyecta con
   `Worker.ConLiquidadorTorneo` después de `NuevoWorker`; sin él, el worker se
   comporta como hoy. El cambio en `liquidacion.go` es mínimo y localizado para
   no chocar con el trabajo de otros en ese archivo.
5. **Puntos = 0 al liquidar torneo en MongoDB.** El contexto se marca liquidado
   para no reprocesarlo, pero el torneo no usa puntos; el valor real vive en
   Redis. La validación de coherencia de puntos de HU-07 se omite para torneo.

## Manejo de errores e idempotencia

- Reservar o liquidar la misma apuesta dos veces no cambia las fichas.
- Reservar con otro monto/opción sobre una apuesta ya existente se rechaza
  (`ErrReservaIncompatible`): protege contra claves reutilizadas con otro
  contenido.
- Sin fichas suficientes la apuesta se rechaza (quien llega a 0 no apuesta).
- Si el envío del WebSocket falla (jugador desconectado, hub detenido) solo se
  registra en el log: las fichas ya quedaron en Redis y no se reintenta por eso.
- Sin `REDIS_URL` el torneo queda deshabilitado y las apuestas de torneo quedan
  reintentables; nunca se liquida un torneo en tokens.

## Pruebas ejecutadas

En `services/motor`, dentro de un contenedor `golang:1.25-alpine`:

```
go build ./...   # compila
go vet ./...     # sin observaciones
go test ./...    # todos los paquetes OK, incluidos internal/fichas e internal/liquidacion
```

Pruebas nuevas (`internal/fichas`):

- `fichas_test.go`: inicialización idempotente; reserva que descuenta una sola
  vez aun con reintento; rechazo por contenido distinto; rechazo sin fichas;
  liquidación que paga `monto × multiplicador` y no paga doble; perdedora que no
  devuelve nada; liquidar sin reserva; torneo no activo.
- `ranking_test.go`: orden por fichas; empates que comparten puesto y el
  siguiente grupo toma el puesto que sigue (1,1,1,2);
  elegibilidad y barra de avance (0 %, 50 %, 100 % y tope); redondeo hacia
  arriba del mínimo.

Las pruebas usan un doble en memoria de `Store`, así que no necesitan Redis. La
ruta real contra Redis se valida en la integración con Docker Compose (pendiente
de la demo conjunta).

## Límites conocidos

- La **UI** del ranking y la barra en React queda como paso siguiente de la HU;
  el contrato (tipos) ya está en el front.
- El **disparo de la largada** (llamar al `start` de Pagos cuando llega el tiempo
  de carrera) es de HU2-07. Hasta que exista, el torneo se inicializa a mano para
  probar.
- La **reserva de fichas al apostar** la invoca HU2-09; esta HU entrega la
  operación pero no cambia el handler de apuestas (para no invadir esa historia).
- El `multiplicador` de la apuesta de torneo debe llegar en el contexto o en la
  reserva; si ambos faltan, la apuesta ganadora pagaría 0 (se asume que HU2-09 lo
  guarda al reservar).
- Recrear la base/estado: las claves de Redis se limpian con `Almacen.Limpiar`
  (HU2-12) o expiran a las 24 h; para una demo limpia, `docker compose down -v`.

## Requisitos de referencia

Pagar las apuestas de torneo en fichas (`monto × multiplicador` si acierta, nada
si falla) sin tocar el saldo en tokens; mantener un ranking por fichas
actualizado en vivo (< 1 s) con empates y una barra hacia el mínimo del 50 %
apostado; y garantizar que repetir liquidaciones o apostar dos veces no duplica
fichas, escribiendo el resultado por apuesta y recalculando el total en Redis.
