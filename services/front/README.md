# Front de PoleBet (React + Vite + TypeScript)

Capa de presentación. No sabe de dónde vienen los datos: solo habla con Pagos y con el motor por REST, y con el motor por WebSocket.

## Estructura

```
index.html
vite.config.ts            # proxy de desarrollo y configuración de Vitest
scripts/mock-ws.mjs       # motor de mentira para desarrollar sin el motor de Go
scripts/dev-jwt.mjs       # firma un JWT de desarrollo (mientras no hay login)
src/
  main.tsx                # proveedores: router, sesión y WebSocket
  App.tsx                 # rutas
  estilos/tokens.css      # paleta, tipografía y medidas (variables CSS)
  estilos/base.css        # clases compartidas: .tarjeta, .boton, .etiqueta...
  api/config.ts           # URLs de los backends
  api/http.ts             # wrapper de fetch (cookie, 401, 403, errores)
  api/ws.ts               # cliente WebSocket con reconexión
  api/WsContexto.tsx      # una sola conexión para toda la app: useWs()
  api/tipos.ts            # tipos de mensajes y respuestas
  auth/AuthContext.tsx    # sesión: useAuth() (simulada hasta HU-02)
  auth/RutaProtegida.tsx  # protege rutas por sesión y rol
  componentes/            # Layout (header) y Pendiente (placeholder)
  paginas/                # una pantalla por ruta
  posiciones/             # HU-05: posiciones en vivo
  apuestas/               # HU-06: apostar (JavaScript)
```

## Rutas

| Ruta | Pantalla | Historia | Quién entra |
| --- | --- | --- | --- |
| `/registro` | Registro | HU-01 | todos |
| `/login` | Inicio de sesión | HU-02 | todos |
| `/carrera` | Posiciones en vivo y mercado | HU-05, HU-06, HU-08 | jugador |
| `/billetera` | Saldo y compra de tokens | HU-03 | jugador |
| `/admin` | Abrir mercados | HU-11, HU-12 | admin |
| `/estado` | Diagnóstico de backends y WebSocket | HU-10 | todos |

Las pantallas sin construir muestran «Pendiente: HU-XX». Para completar una, reemplaza el contenido de su archivo en `src/paginas/`; el header y el estilo ya vienen del `Layout`. Si agregas una pantalla nueva, regístrala en `App.tsx` y, si va en el menú, en `ENLACES` de `componentes/Layout.tsx`.

## Correr

Necesitas Node 20 o superior.

```bash
cd services/front
npm install
npm run dev        # http://localhost:5173, contra los backends reales
```

Sin Node instalado, lo mismo con Docker:

```bash
docker run --rm -it --network host -u $(id -u):$(id -g) -e HOME=/tmp \
  -v "$PWD":/app -w /app node:22-alpine sh -c "npm install && npm run dev"
```

| Comando | Qué hace |
| --- | --- |
| `npm run dev` | Servidor de desarrollo contra los backends reales |
| `npm run dev:mock` | Igual, pero el motor es el mock |
| `npm run mock` | Levanta el mock del motor en el puerto 8090 |
| `npm run jwt` | Firma un JWT de desarrollo |
| `npm test` | Vitest (`*.test.ts(x)`) y la prueba de HU-06 con `node --test` |
| `npm run build` | Revisa los tipos y compila a `dist/` |

## Cómo se conecta con los backends

En desarrollo el front llama a `/api/pagos/...` y `/api/motor/...` en su mismo origen, y el proxy de Vite reenvía a `localhost:8000` y `localhost:8080` quitando el prefijo. Así la cookie de sesión viaja sola, también al abrir el WebSocket.

En Docker no hay proxy: `docker-compose.yml` inyecta las URLs absolutas (`VITE_PAGOS_URL`, `VITE_MOTOR_URL`, `VITE_MOTOR_WS_URL`) al construir la imagen.

```ts
// Las rutas son de ejemplo.
import { http } from "../api/http";

const saldo = await http.get<Saldo>("pagos", "/billetera");          // GET  {pagos}/billetera
const compra = await http.post<Compra>("pagos", "/compras", { cop }); // POST con cuerpo JSON
```

`http` siempre envía la cookie y, si algo falla, lanza un `ErrorHttp` con `status` y `message` en español (`status` 0 = no hubo respuesta). Un 401 cierra la sesión, lo que lleva a `/login`; un 403 muestra un aviso de acceso denegado.

## Variables de entorno

Opcionales. Copia `.env.example` a `.env` dentro de `services/front/` para cambiarlas.

| Variable | Por defecto | Para qué |
| --- | --- | --- |
| `VITE_PAGOS_URL` | `/api/pagos` (proxy) | URL de Pagos |
| `VITE_MOTOR_URL` | `/api/motor` (proxy) | URL REST del motor |
| `VITE_MOTOR_WS_URL` | mismo origen | URL WebSocket del motor, sin `/ws` |
| `VITE_SHOW_LATENCY` | `false` | `true` muestra la latencia en `/carrera` |
| `VITE_ROL_SIMULADO` | `player` | Sesión simulada al arrancar: `player`, `admin` o `ninguno` |

Solo para el servidor de desarrollo: `PROXY_MOTOR` y `PROXY_PAGOS` cambian a dónde reenvía el proxy.

## Sesión (simulada por ahora)

Mientras no exista el login (HU-02), `AuthContext` arranca con un usuario de mentira según `VITE_ROL_SIMULADO`, y `/login` tiene dos botones para entrar como jugador o como admin. Quien implemente HU-02 solo cambia `auth/AuthContext.tsx`: el resto de la app usa `useAuth()`.

El usuario simulado no tiene cookie, así que el motor real rechaza su WebSocket. Para probar contra el motor real, firma un token:

```bash
npm run jwt                    # jugador, 120 minutos
npm run jwt -- --rol admin
npm run jwt -- --min 1         # vence en un minuto
```

El script imprime el token y la línea para pegarlo como cookie `session` en la consola del navegador. Toma el secreto de `JWT_SECRET` o del `.env` de la raíz del repo. Es solo para desarrollo.

## WebSocket

Hay una sola conexión para toda la app. Se abre al iniciar sesión y, si se cae, se reconecta sola con espera de 1, 2, 4, 8 y luego 10 segundos.

```tsx
const { estado, suscribir } = useWs(); // estado: conectando | abierta | reconectando | cerrada

useEffect(() => suscribir<MiPayload>(TIPOS_WS.mercadoAbierto, (payload) => { /* ... */ }), [suscribir]);
```

`suscribir` devuelve la función para desuscribirse, por eso se puede retornar directo desde el `useEffect`. Los mensajes llegan con el sobre del motor, `{ "type": "...", "payload": { ... } }`, y los tipos están en `TIPOS_WS` (`api/tipos.ts`), con los mismos nombres de `services/motor/internal/ws/mensajes.go`.

## HU-05: posiciones en vivo

`/carrera` pide primero `GET /posiciones` al motor y luego escucha el mensaje `posiciones`. Cuando el WebSocket vuelve de una caída, pide la foto otra vez. Mientras no hay conexión se ve el aviso «Reconectando…», y la fila de un piloto que cambia de puesto se resalta un momento.

Formato de la foto (respuesta del `GET` y `payload` del mensaje). **Es una propuesta**: el esquema final depende de HU-04; cuando se cierre se ajusta en `api/tipos.ts` y en el mock.

```json
{
  "carrera_id": "2026_costa_azul",
  "vuelta": 18,
  "actualizado_en": "2026-10-04T18:30:00.000Z",
  "emitido_en": "2026-10-04T18:30:00.120Z",
  "pilotos": [
    { "posicion": 1, "numero": 4, "codigo": "VAR", "nombre": "L. Varga", "equipo": "Scuderia Roja", "color_equipo": "#E10600", "diferencia": "Líder" }
  ]
}
```

`emitido_en` es el momento en que el motor envía el mensaje; con `VITE_SHOW_LATENCY=true` la pantalla muestra cuánto tardó en llegar (RNF-04). `vuelta`, `color_equipo` y `emitido_en` son opcionales.

Falta en el motor: el módulo `posiciones` (`GET /posiciones` y la difusión del mensaje). Hasta entonces, HU-05 se prueba con el mock.

### Mock del motor

```bash
npm run mock        # terminal 1: motor falso en http://localhost:8090
npm run dev:mock    # terminal 2: Vite con el proxy del motor apuntando al mock
```

El mock responde `GET /posiciones` y `GET /health`, y emite `posiciones` cada 2 segundos con un adelantamiento al azar. No valida la sesión. Para ver la reconexión, detén el mock con Ctrl+C y vuelve a levantarlo. `MOCK_PORT` y `MOCK_INTERVALO_MS` cambian el puerto y el ritmo.

#### Sin Node instalado (solo Docker)

Desde `services/front`, en una terminal de Linux, macOS o WSL. Primero el front, que también instala las dependencias:

```bash
# Terminal 1: front en modo mock -> http://localhost:5173
docker run --rm -it --name polebet-front -p 5173:5173 \
  -u $(id -u):$(id -g) -e HOME=/tmp -e VITE_SHOW_LATENCY=true \
  -v "$PWD":/app -w /app node:22-alpine sh -c "npm install && npm run dev:mock"
```

Cuando Vite diga que está listo, levanta el mock en otra terminal. Comparte la red del contenedor del front, así el proxy lo encuentra en `localhost:8090`:

```bash
# Terminal 2: mock del motor
docker run --rm -it --name polebet-mock --network container:polebet-front \
  -u $(id -u):$(id -g) -e HOME=/tmp \
  -v "$PWD":/app -w /app node:22-alpine npm run mock
```

Abre http://localhost:5173. Para ver la reconexión, detén la terminal 2 con Ctrl+C y vuelve a correr su comando. Al terminar, detén primero el mock y luego el front.

## Estilo

Los colores, fuentes y medidas salen de los mockups del equipo y están en `estilos/tokens.css`. Usa esas variables (`var(--accent)`, `var(--surface)`...) y las clases de `estilos/base.css` en vez de valores sueltos. Para estilos propios de un componente, usa un CSS Module (`MiComponente.module.css`). Los botones y enlaces táctiles miden al menos 44 px de alto (`var(--tap)`).
