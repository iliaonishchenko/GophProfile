# GophProfile

GophProfile микросервис для управления аватарками пользователей написанный с использованием Go, Chi, PostgreSQL,
RabbitMQ, and MinIO.

## локальный запуск

запуск в докере следующей командой

```bash
docker compose up --build
```

- Web UI and API: <http://localhost:8080>
- RabbitMQ: <http://localhost:15672>
- MinIO: <http://localhost:9001>
- PostgreSQL: `localhost:5432`
- Jaeger: <http://localhost:16686>
- Prometheus: <http://localhost:9090>
- Grafana: <http://localhost:3000> (`admin` / `admin`)
- OpenSearch Dashboards: <http://localhost:5601>

дефолтные креды в `compose.yaml`.

## команды

```bash
task generate-server  # сгенерировать сервер и модели из OpenAPI
task generate-mocks   # сгенерировать mock-реализации доменных интерфейсов
task test             # запуск тестов
task cover            # запуск юнит тестов с измерением покрытия
task vet              # запуск go vet
task lint             # запуск golangci-lint
task build            # собрать server и worker
task run              # запуск HTTP server
task run-worker       # запуск image воркера
```

## процесс обработки

1. API валидирует и загружает изображение в MinIO
2. метаданные сохраняются в PostgreSQL
3. `avatar.uploaded` событие публикуется в RabbitMQ топик
4. воркер создаёт `100x100` и `300x300` JPEG thumbnails
5. ключи и статус сохраняются в PostgreSQL.
6. удаление делает софт-делет метаданнх и публикует событие `avatar.deleted`

## observability

HTTP-запросы, операции PostgreSQL и MinIO, публикация RabbitMQ и обработка
сообщений worker-ом входят в один distributed trace. W3C `traceparent` передаётся
в заголовках RabbitMQ, поэтому в Jaeger видна цепочка от API до фоновой обработки.

Prometheus собирает метрики с `server:8080/metrics` и `worker:9091/metrics`.
Grafana автоматически загружает дашборд `GophProfile Overview` с HTTP rate,
p95 latency, загрузками, удалениями и результатами обработки worker-а.

Приложения пишут JSON-логи с полями `service`, `trace_id` и `span_id`.
Fluent Bit читает Docker-логи и индексирует их в OpenSearch с шаблоном
`gophprofile-logs-*`. Для поиска в OpenSearch Dashboards создайте data view
`gophprofile-logs-*`; по `trace_id` можно перейти от трейса к связанным логам.
