// Hooks de HU-11.
//
// useMercado entrega el mercado actual siempre al dia:
//   1. Al montar lo pide por REST (GET /mercados/actual), para que quien entra
//      con un mercado ya abierto lo vea sin esperar un mensaje.
//   2. Luego escucha mercado_abierto, mercado_cerrado y mercado_liquidado.
//   3. Si el WebSocket se cae y vuelve, lo pide otra vez para ponerse al dia.
//
// useRelojServidor da la hora del servidor (reloj local + desfase) y se
// actualiza 4 veces por segundo para la cuenta regresiva.

import { useCallback, useEffect, useRef, useState } from "react";
import { TIPOS_WS, type Mercado } from "../api/tipos";
import { useWs } from "../api/WsContexto";
import { obtenerMercadoActual } from "./api";
import { desfaseDe, esMercado, reemplaza } from "./mercado";

export interface MercadoActual {
  mercado: Mercado | null;
  cargando: boolean;
  error: string | null;
  desfaseMs: number; // sumar al reloj local para tener la hora del servidor
  aplicar: (m: Mercado) => void; // p. ej. con la respuesta de POST /mercados
  recargar: () => void;
}

export function useMercado(): MercadoActual {
  const { estado, suscribir } = useWs();
  const [mercado, setMercado] = useState<Mercado | null>(null);
  const [cargando, setCargando] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [desfaseMs, setDesfaseMs] = useState(0);

  const actual = useRef<Mercado | null>(null);
  const montado = useRef(true);
  const seCayo = useRef(false);

  useEffect(() => {
    montado.current = true;
    return () => {
      montado.current = false;
    };
  }, []);

  const aplicar = useCallback((m: Mercado) => {
    if (!reemplaza(m, actual.current)) return;
    actual.current = m;
    setMercado(m);
    setDesfaseMs(desfaseDe(m));
    setError(null);
  }, []);

  const cargar = useCallback(async () => {
    try {
      const m = await obtenerMercadoActual();
      if (!montado.current) return;
      if (m) aplicar(m);
    } catch (causa) {
      if (!montado.current) return;
      // Si ya hay un mercado en pantalla se deja: el WebSocket lo actualiza.
      if (!actual.current) setError(causa instanceof Error ? causa.message : "No se pudo cargar el mercado.");
    } finally {
      if (montado.current) setCargando(false);
    }
  }, [aplicar]);

  // Paso 1: carga inicial.
  useEffect(() => {
    void cargar();
  }, [cargar]);

  // Paso 2: mensajes en vivo.
  useEffect(() => {
    const alRecibir = (payload: unknown) => {
      if (esMercado(payload)) aplicar(payload);
    };
    const quitar = [
      suscribir<unknown>(TIPOS_WS.mercadoAbierto, alRecibir),
      suscribir<unknown>(TIPOS_WS.mercadoCerrado, alRecibir),
      // HU-07 aun no fija su payload: si trae el mercado completo se usa; si
      // solo trae el id, se marca liquidado el que esta en pantalla.
      suscribir<unknown>(TIPOS_WS.mercadoLiquidado, (payload) => {
        if (esMercado(payload)) return aplicar(payload);
        const p = (payload ?? {}) as { id?: unknown; mercado_id?: unknown };
        const id = p.id ?? p.mercado_id;
        const m = actual.current;
        if (m && id === m.id) {
          const liquidado: Mercado = { ...m, estado: "liquidado" };
          actual.current = liquidado;
          setMercado(liquidado);
        }
      }),
    ];
    return () => quitar.forEach((f) => f());
  }, [suscribir, aplicar]);

  // Paso 3: al volver de una caida, ponerse al dia.
  useEffect(() => {
    if (estado === "reconectando") seCayo.current = true;
    if (estado === "abierta" && seCayo.current) {
      seCayo.current = false;
      void cargar();
    }
  }, [estado, cargar]);

  const recargar = useCallback(() => {
    setCargando(true);
    setError(null);
    void cargar();
  }, [cargar]);

  return { mercado, cargando, error, desfaseMs, aplicar, recargar };
}

// Hora del servidor en ms, actualizada cada 250 ms.
export function useRelojServidor(desfaseMs: number): number {
  const [ahora, setAhora] = useState(() => Date.now() + desfaseMs);
  useEffect(() => {
    setAhora(Date.now() + desfaseMs);
    const id = setInterval(() => setAhora(Date.now() + desfaseMs), 250);
    return () => clearInterval(id);
  }, [desfaseMs]);
  return ahora;
}
