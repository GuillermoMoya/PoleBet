package config

import (
	"strings"
	"testing"
	"time"
)

func entorno(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func entornoCompleto() map[string]string {
	return map[string]string{
		"MONGO_URI":          "mongodb://localhost:27017",
		"MONGO_DB":           "apuestas_db",
		"JWT_SECRET":         "secreto",
		"PAGOS_BASE_URL":     "http://pagos:8000",
		"INTERNAL_API_TOKEN": "token-interno-de-pruebas-para-hu07",
		"ALLOWED_ORIGINS":    "http://localhost:5173, http://127.0.0.1:5173",
	}
}

func TestCargaConValoresPorDefecto(t *testing.T) {
	cfg, err := desde(entorno(entornoCompleto()))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q, se esperaba 8080", cfg.HTTPPort)
	}
	if cfg.VentanaApuestas != 30*time.Second || cfg.VentanaObservacion != 60*time.Second {
		t.Errorf("ventanas = %v / %v, se esperaba 30s / 60s", cfg.VentanaApuestas, cfg.VentanaObservacion)
	}
	if len(cfg.OrigenesPermitidos) != 2 || cfg.OrigenesPermitidos[1] != "http://127.0.0.1:5173" {
		t.Errorf("origenes = %v", cfg.OrigenesPermitidos)
	}
}

func TestFallaSiFaltanObligatorias(t *testing.T) {
	vars := entornoCompleto()
	delete(vars, "JWT_SECRET")
	delete(vars, "MONGO_URI")

	_, err := desde(entorno(vars))
	if err == nil {
		t.Fatal("se esperaba error por variables faltantes")
	}
	for _, clave := range []string{"JWT_SECRET", "MONGO_URI"} {
		if !strings.Contains(err.Error(), clave) {
			t.Errorf("el error no menciona %s: %v", clave, err)
		}
	}
}

func TestOrigenesVacios(t *testing.T) {
	vars := entornoCompleto()
	vars["ALLOWED_ORIGINS"] = " , ,"
	if _, err := desde(entorno(vars)); err == nil {
		t.Fatal("se esperaba error con ALLOWED_ORIGINS sin origenes")
	}
}

func TestVentanaInvalida(t *testing.T) {
	for _, v := range []string{"abc", "0", "-5"} {
		vars := entornoCompleto()
		vars["BETTING_WINDOW_SECONDS"] = v
		if _, err := desde(entorno(vars)); err == nil {
			t.Errorf("BETTING_WINDOW_SECONDS=%q deberia fallar", v)
		}
	}
}

func TestTokenInternoCorto(t *testing.T) {
	vars := entornoCompleto()
	vars["INTERNAL_API_TOKEN"] = "demasiado-corto"
	if _, err := desde(entorno(vars)); err == nil {
		t.Fatal("se esperaba rechazo de INTERNAL_API_TOKEN corto")
	}
}
