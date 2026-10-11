// Pantalla /billetera (HU-03): saldo y compra de tokens simulados. Sigue el
// mockup "Billetera": arriba el saldo disponible y debajo la tarjeta
// "Consignar" (monto en COP, atajos, medio de pago y tokens que se reciben).
//
// Criterios de aceptacion que cubre:
//   - Elijo un monto en COP, confirmo y veo mi saldo nuevo (aqui y en el header).
//   - Un monto <= 0 se rechaza (el boton no se habilita y Pagos tambien lo valida).
//   - Doble clic o reintento: los tokens se acreditan una sola vez (RNF-02).
//     El boton se bloquea mientras se envia y cada intento de compra lleva
//     una Idempotency-Key que se reutiliza si hay que reintentar.

import { useRef, useState, type FormEvent } from "react";
import { ErrorHttp } from "../api/http";
import { validarMonto } from "../billetera/api";
import { useBilletera } from "../billetera/BilleteraContexto";
import estilos from "./Billetera.module.css";

// Atajos de monto del mockup, en COP.
const ATAJOS = [20_000, 50_000, 100_000];

// Medios de pago. Son solo informativos: el pago se aprueba sin pasarela real.
const MEDIOS = ["Transferencia", "Tarjeta", "Billetera digital"] as const;
type Medio = (typeof MEDIOS)[number];

const numero = new Intl.NumberFormat("es-CO");

// "50000" -> "$50.000"; vacio -> "".
function formatoCop(digitos: string): string {
  return digitos === "" ? "" : `$${numero.format(Number(digitos))}`;
}

// Clave unica para un intento de compra. crypto.randomUUID solo existe en
// contextos seguros (https o localhost); si se abre por la IP de la red local
// se arma una con getRandomValues.
function nuevaClave(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, "0")).join("");
}

interface Mensaje {
  ok: boolean;
  texto: string;
}

export default function Billetera() {
  const { saldo, copPorToken, error, recargar, comprar } = useBilletera();
  const [digitos, setDigitos] = useState("50000"); // monto en COP, solo digitos
  const [medio, setMedio] = useState<Medio>("Transferencia");
  const [enviando, setEnviando] = useState(false);
  const [mensaje, setMensaje] = useState<Mensaje | null>(null);
  // Clave del intento en curso. Se conserva si la compra falla, para que el
  // reintento no acredite dos veces si la primera si llego a Pagos.
  const clave = useRef<string | null>(null);

  const montoCop = digitos === "" ? 0 : Number(digitos);
  const errorMonto = copPorToken === null || digitos === "" ? null : validarMonto(montoCop, copPorToken);
  const tokens = copPorToken && !errorMonto ? montoCop / copPorToken : 0;
  const puedeComprar = tokens > 0 && !enviando;

  function cambiarMonto(texto: string) {
    // Se guardan solo los digitos: el campo muestra el formato "$50.000".
    setDigitos(texto.replace(/\D/g, "").replace(/^0+/, "").slice(0, 9));
    setMensaje(null);
    clave.current = null; // otro monto = otra compra
  }

  async function onSubmit(evento: FormEvent<HTMLFormElement>) {
    evento.preventDefault();
    if (!puedeComprar) return; // tambien evita el doble envio con doble clic

    clave.current ??= nuevaClave();
    setEnviando(true);
    setMensaje(null);
    try {
      const compra = await comprar(montoCop, clave.current);
      clave.current = null; // compra terminada: la siguiente lleva clave nueva
      setMensaje({ ok: true, texto: `Compra aprobada: recibiste ${numero.format(compra.tokens_acreditados)} PBT.` });
    } catch (causa) {
      // ErrorHttp ya trae el mensaje en español: monto invalido (422), red
      // caida (status 0), etc.
      setMensaje({ ok: false, texto: causa instanceof ErrorHttp ? causa.message : "Ocurrió un error inesperado." });
    } finally {
      setEnviando(false);
    }
  }

  return (
    <div className={estilos.pagina}>
      <h1 className="solo-lectores">Billetera</h1>

      {/* Saldo disponible */}
      <section className={`tarjeta ${estilos.saldo}`} aria-labelledby="titulo-saldo">
        <h2 id="titulo-saldo" className={estilos.etiquetaSaldo}>
          Saldo disponible
        </h2>
        {error ? (
          <div role="alert" className={estilos.errorSaldo}>
            <p>{error}</p>
            <button type="button" className="boton boton-secundario" onClick={recargar}>
              Reintentar
            </button>
          </div>
        ) : (
          <>
            <p className={estilos.cifra} data-testid="saldo">
              {saldo === null ? "—" : numero.format(saldo)} <span className={estilos.moneda}>PBT</span>
            </p>
            {saldo !== null && copPorToken !== null && (
              <p className="texto-secundario">
                ≈ ${numero.format(saldo * copPorToken)} COP · 1 PBT = ${numero.format(copPorToken)} COP (valor indexado al USD)
              </p>
            )}
          </>
        )}
      </section>

      {/* Consignar */}
      <section className={`tarjeta ${estilos.consignar}`} aria-labelledby="titulo-consignar">
        <h2 id="titulo-consignar" className={estilos.tituloConsignar}>
          Consignar
        </h2>

        <form className={estilos.formulario} onSubmit={onSubmit} noValidate>
          <div className={estilos.campo}>
            <label htmlFor="billetera-monto">Monto en pesos (COP)</label>
            <input
              id="billetera-monto"
              inputMode="numeric"
              autoComplete="off"
              placeholder="$0"
              value={formatoCop(digitos)}
              onChange={(e) => cambiarMonto(e.target.value)}
              aria-invalid={errorMonto ? true : undefined}
              aria-describedby={errorMonto ? "billetera-monto-error" : undefined}
              disabled={enviando}
            />
            {errorMonto && (
              <span id="billetera-monto-error" className={estilos.errorCampo}>
                {errorMonto}
              </span>
            )}
          </div>

          <div className={estilos.atajos} role="group" aria-label="Montos sugeridos">
            {ATAJOS.map((monto) => (
              <button
                key={monto}
                type="button"
                className={montoCop === monto ? `${estilos.atajo} ${estilos.atajoActivo}` : estilos.atajo}
                aria-pressed={montoCop === monto}
                onClick={() => cambiarMonto(String(monto))}
                disabled={enviando}
              >
                {formatoCop(String(monto))}
              </button>
            ))}
          </div>

          <fieldset className={estilos.medios} disabled={enviando}>
            <legend>Medio de pago</legend>
            <div className={estilos.opcionesMedio}>
              {MEDIOS.map((m) => (
                <button
                  key={m}
                  type="button"
                  className={medio === m ? `${estilos.medio} ${estilos.medioActivo}` : estilos.medio}
                  aria-pressed={medio === m}
                  onClick={() => setMedio(m)}
                >
                  {m}
                </button>
              ))}
            </div>
          </fieldset>

          <div className={estilos.recibes}>
            <span className="texto-secundario">Recibes</span>
            <b>{numero.format(tokens)} PBT</b>
          </div>

          <button type="submit" className={`boton ${estilos.comprar}`} disabled={!puedeComprar}>
            {enviando ? "Comprando…" : "Comprar tokens"}
          </button>

          <div aria-live="polite">
            {mensaje && (
              <p className={mensaje.ok ? estilos.mensaje : estilos.mensajeError} role={mensaje.ok ? undefined : "alert"}>
                {mensaje.texto}
              </p>
            )}
          </div>
        </form>
      </section>
    </div>
  );
}
