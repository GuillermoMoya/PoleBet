// ============================================================
// 02-seed-data.js
// Datos de prueba para el Prototipo 1.
// Corre después de 01-init-collections.js (orden alfabético espero).
// ============================================================

db = db.getSiblingDB('apuestas_db');

print('🌱 Insertando datos de prueba...');

// ------------------------------------------------------------
// 1. CARRERA DE PRUEBA
// ------------------------------------------------------------
const carreraId = '2024_monza';

db.carreras.updateOne(
  { carrera_id: carreraId },
  {
    $setOnInsert: {
      carrera_id: carreraId,
      nombre: 'Gran Premio de Italia 2024',
      temporada: 2024,
      circuito: 'Autodromo Nazionale Monza',
      fuente: 'openf1',
      estado: 'cargada',
      creada_en: new Date()
    }
  },
  { upsert: true }
);
print('  ✅ Carrera insertada: ' + carreraId);

// ------------------------------------------------------------
// 2. EVENTOS DE CARRERA (simulan lo que vendría de OpenF1)
// ------------------------------------------------------------
const baseTime = new Date('2024-09-01T13:00:00Z');

const eventos = [
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 0),
    tipo: 'posicion',
    datos: {
      driver_number: 1,
      driver_name: 'Max Verstappen',
      position: 1,
      session_key: 'monza_2024'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 1000),
    tipo: 'posicion',
    datos: {
      driver_number: 4,
      driver_name: 'Lando Norris',
      position: 2,
      session_key: 'monza_2024'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 2000),
    tipo: 'posicion',
    datos: {
      driver_number: 16,
      driver_name: 'Charles Leclerc',
      position: 3,
      session_key: 'monza_2024'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 3000),
    tipo: 'posicion',
    datos: {
      driver_number: 81,
      driver_name: 'Oscar Piastri',
      position: 4,
      session_key: 'monza_2024'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 4000),
    tipo: 'posicion',
    datos: {
      driver_number: 63,
      driver_name: 'George Russell',
      position: 5,
      session_key: 'monza_2024'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 5000),
    tipo: 'vuelta',
    datos: {
      lap_number: 1,
      lap_time: 83.5,
      driver_number: 1
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 6000),
    tipo: 'bandera',
    datos: {
      flag: 'GREEN',
      scope: 'track'
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 7000),
    tipo: 'clima',
    datos: {
      air_temperature: 26.5,
      track_temperature: 38.2,
      humidity: 45,
      rainfall: 0
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 8000),
    tipo: 'sector',
    datos: {
      driver_number: 1,
      sector: 1,
      time: 28.4
    },
    leido_en: new Date()
  },
  {
    carrera_id: carreraId,
    timestamp: new Date(baseTime.getTime() + 9000),
    tipo: 'pit_stop',
    datos: {
      driver_number: 16,
      duration: 2.4,
      lap: 15
    },
    leido_en: new Date()
  }
];

db.eventos_carrera.insertMany(eventos, { ordered: false });
print('  ✅ ' + eventos.length + ' eventos insertados');

// No se siembra ningún mercado: los abre el admin desde /admin (HU-11).
// Un mercado sembrado nacía con la ventana corriendo desde el arranque de
// MongoDB: el motor lo cerraba al iniciar, el worker de HU-07 lo reintentaba
// cada 2 s para siempre (sin reproducción no hay forma de liquidarlo) y
// aparecía como mercado actual en /admin.

print('');
print('========================================');
print('✅ Datos de prueba insertados');
print('  - Carrera: ' + carreraId);
print('  - Eventos: ' + eventos.length);
print('  - Mercados: ninguno (los abre el admin)');
print('========================================');