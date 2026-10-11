// Saldo del jugador para toda la app (HU-03): lo muestran el header y la
// pantalla /billetera, y una compra lo actualiza en los dos a la vez.
//
//   const { saldo, copPorToken, comprar } = useBilletera();
//
// Solo se pide a Pagos cuando hay un jugador con sesion: el admin no tiene
// billetera (HU-12) y sin sesion Pagos responderia 401.

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useAuth } from "../auth/AuthContext";
import { comprarTokens, obtenerBilletera, type Compra } from "./api";

export interface ValorBilletera {
  saldo: number | null; // null mientras carga, sin jugador o si fallo la carga
  copPorToken: number | null;
  error: string | null; // fallo al cargar el saldo
  recargar: () => void;
  comprar: (montoCop: number, clave: string) => Promise<Compra>; // lanza ErrorHttp si falla
}

const BilleteraContexto = createContext<ValorBilletera | null>(null);

export function BilleteraProvider({ children }: { children: ReactNode }) {
  const { usuario } = useAuth();
  const esJugador = usuario?.rol === "player";
  const userId = usuario?.id;

  const [saldo, setSaldo] = useState<number | null>(null);
  const [copPorToken, setCopPorToken] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [version, setVersion] = useState(0); // cambia para forzar una recarga

  // Carga el saldo al entrar un jugador; lo limpia al salir.
  useEffect(() => {
    setSaldo(null);
    setCopPorToken(null);
    setError(null);
    if (!esJugador) return;

    // AbortController: no tocar el estado si el usuario sale antes de la respuesta.
    const control = new AbortController();
    obtenerBilletera(control.signal)
      .then((b) => {
        setSaldo(b.saldo);
        setCopPorToken(b.cop_por_token);
      })
      .catch((causa: unknown) => {
        if (causa instanceof DOMException && causa.name === "AbortError") return;
        setError(causa instanceof Error ? causa.message : "No se pudo cargar el saldo.");
      });
    return () => control.abort();
  }, [esJugador, userId, version]);

  const recargar = useCallback(() => setVersion((v) => v + 1), []);

  // Pagos devuelve el saldo ya actualizado: se usa ese, no una suma local.
  const comprar = useCallback(async (montoCop: number, clave: string) => {
    const compra = await comprarTokens(montoCop, clave);
    setSaldo(compra.saldo);
    return compra;
  }, []);

  const valor = useMemo<ValorBilletera>(
    () => ({ saldo, copPorToken, error, recargar, comprar }),
    [saldo, copPorToken, error, recargar, comprar],
  );

  return <BilleteraContexto.Provider value={valor}>{children}</BilleteraContexto.Provider>;
}

// Hook para leer el saldo y comprar.
export function useBilletera(): ValorBilletera {
  const valor = useContext(BilleteraContexto);
  if (!valor) throw new Error("useBilletera debe usarse dentro de <BilleteraProvider>");
  return valor;
}
