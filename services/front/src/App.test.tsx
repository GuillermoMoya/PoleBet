// Pruebas de las rutas: placeholders, proteccion por sesion y por rol.
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WsProvider } from "./api/WsContexto";
import { ClienteWs, type SocketLike } from "./api/ws";
import App from "./App";
import { AuthProvider, USUARIOS_SIMULADOS, type Usuario } from "./auth/AuthContext";

// Socket que nunca abre: aqui no interesa el WebSocket.
const socketMudo = (): SocketLike => ({ onopen: null, onmessage: null, onclose: null, onerror: null, close() {} });

// Monta la app en la ruta dada con el usuario dado (null = sin sesion).
function abrir(ruta: string, usuario: Usuario | null) {
  render(
    <MemoryRouter initialEntries={[ruta]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider usuarioInicial={usuario}>
        <WsProvider cliente={new ClienteWs({ url: "ws://prueba/ws", crearSocket: socketMudo })}>
          <App />
        </WsProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

// Las pantallas que piden datos al montar reciben un 404 inofensivo.
beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 404, json: async () => ({}) })),
  );
});

describe("rutas", () => {
  it.each([
    ["/registro", "Crear cuenta", "Pendiente: HU-01"],
    ["/login", "Iniciar sesión", "Pendiente: HU-02"],
  ])("%s es publica y muestra su placeholder", (ruta, titulo, pendiente) => {
    abrir(ruta, null);
    expect(screen.getByRole("heading", { name: titulo })).toBeInTheDocument();
    expect(screen.getByText(pendiente)).toBeInTheDocument();
  });

  it("sin sesion, una ruta protegida lleva a /login", () => {
    abrir("/carrera", null);
    expect(screen.getByRole("heading", { name: "Iniciar sesión" })).toBeInTheDocument();
  });

  it("el jugador ve la carrera y la billetera", () => {
    abrir("/billetera", USUARIOS_SIMULADOS.player);
    expect(screen.getByText("Pendiente: HU-03")).toBeInTheDocument();
  });

  it("el jugador no puede entrar a /admin", () => {
    abrir("/admin", USUARIOS_SIMULADOS.player);
    expect(screen.getByRole("heading", { name: "Acceso denegado" })).toBeInTheDocument();
    expect(screen.queryByText(/HU-11/)).not.toBeInTheDocument();
  });

  it("el admin entra a /admin y solo ve su enlace en el menu", () => {
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    expect(screen.getByText("Pendiente: HU-11 y HU-12")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Admin" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "En vivo" })).not.toBeInTheDocument();
  });

  it("la raiz lleva al jugador a /carrera", async () => {
    abrir("/", USUARIOS_SIMULADOS.player);
    expect(screen.getByRole("heading", { name: "Carrera en vivo" })).toBeInTheDocument();
    // Espera a que termine la carga inicial de posiciones (el 404 de arriba).
    expect(await screen.findByText("No hay una carrera en curso.")).toBeInTheDocument();
  });

  it("una ruta desconocida muestra la pagina no encontrada", () => {
    abrir("/no-existe", null);
    expect(screen.getByRole("heading", { name: "Página no encontrada" })).toBeInTheDocument();
  });
});
