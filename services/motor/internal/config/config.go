// Package config lee la configuracion del motor desde variables de entorno.
//
// Las variables obligatorias no tienen valor por defecto: si falta alguna,
// el motor no arranca. Asi nunca corre con un secreto de ejemplo escrito en
// el codigo.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MongoURI         string
	MongoDB          string
	HTTPPort         string
	JWTSecret        string
	PagosBaseURL     string
	InternalAPIToken string

	// OrigenesPermitidos son los origenes del front que pueden llamar al
	// motor con cookies (CORS) y abrir el WebSocket.
	OrigenesPermitidos []string

	// Ciclo del mercado (RF-07, RF-09, RNF-05).
	VentanaApuestas    time.Duration
	VentanaObservacion time.Duration
}

// Cargar lee la configuracion del entorno del proceso.
func Cargar() (Config, error) {
	return desde(os.Getenv)
}

// desde recibe la funcion de lectura para poder probarla sin tocar el
// entorno real.
func desde(getenv func(string) string) (Config, error) {
	var faltan []string
	requerida := func(clave string) string {
		v := strings.TrimSpace(getenv(clave))
		if v == "" {
			faltan = append(faltan, clave)
		}
		return v
	}

	cfg := Config{
		MongoURI:     requerida("MONGO_URI"),
		MongoDB:      requerida("MONGO_DB"),
		JWTSecret:    requerida("JWT_SECRET"),
		PagosBaseURL: requerida("PAGOS_BASE_URL"),
		InternalAPIToken: requerida("INTERNAL_API_TOKEN"),
		HTTPPort:     opcional(getenv, "HTTP_PORT", "8080"),
	}
	origenes := separarLista(requerida("ALLOWED_ORIGINS"))

	if len(faltan) > 0 {
		return Config{}, fmt.Errorf("faltan variables de entorno obligatorias: %s", strings.Join(faltan, ", "))
	}
	if len(cfg.InternalAPIToken) < 32 {
		return Config{}, fmt.Errorf("INTERNAL_API_TOKEN debe tener al menos 32 caracteres")
	}
	if len(origenes) == 0 {
		return Config{}, fmt.Errorf("ALLOWED_ORIGINS no tiene ningun origen valido")
	}
	cfg.OrigenesPermitidos = origenes

	var err error
	if cfg.VentanaApuestas, err = segundos(getenv, "BETTING_WINDOW_SECONDS", 30); err != nil {
		return Config{}, err
	}
	if cfg.VentanaObservacion, err = segundos(getenv, "OBSERVATION_WINDOW_SECONDS", 60); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func opcional(getenv func(string) string, clave, def string) string {
	if v := strings.TrimSpace(getenv(clave)); v != "" {
		return v
	}
	return def
}

// segundos lee un entero positivo de segundos; si la variable no existe usa
// el valor por defecto.
func segundos(getenv func(string) string, clave string, def int) (time.Duration, error) {
	v := strings.TrimSpace(getenv(clave))
	if v == "" {
		return time.Duration(def) * time.Second, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s debe ser un entero positivo de segundos, llego %q", clave, v)
	}
	return time.Duration(n) * time.Second, nil
}

// separarLista convierte "a, b,,c" en ["a", "b", "c"].
func separarLista(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
