# GophProfile

GophProfile — сервис управления аватарками на Go. HTTP API сохраняет метаданные
в PostgreSQL и исходные файлы в MinIO, публикует задания в RabbitMQ, а отдельный
worker создаёт JPEG-миниатюры `100x100` и `300x300`.

## Локальный запуск

Нужны Docker и Docker Compose:

```bash
docker compose up --build
```

Перед запуском API и worker Compose один раз выполняет контейнер `migrate`.

- Web UI и API: <http://localhost:8080>
- Liveness: <http://localhost:8080/live>
- Readiness: <http://localhost:8080/health>
- Метрики API: <http://localhost:8080/metrics>
- RabbitMQ: <http://localhost:15672>
- MinIO: <http://localhost:9001>
- PostgreSQL: `localhost:5432`
- Jaeger: <http://localhost:16686>
- Prometheus: <http://localhost:9090>
- Grafana: <http://localhost:3000> (`admin` / `admin`)
- OpenSearch Dashboards: <http://localhost:5601>

Учебные локальные логины и пароли находятся в `compose.yaml`.

## Команды разработки

```bash
task generate-server  # обновить Go-код из OpenAPI
task generate-mocks   # обновить mock-реализации
task test             # запустить тесты
task cover            # проверить покрытие основных пакетов
task vet              # запустить go vet
task lint             # запустить golangci-lint
task build            # собрать server, worker и migrate
task migrate          # применить миграции PostgreSQL
task run              # запустить HTTP API
task run-worker       # запустить worker
```

Актуальный контракт API находится в `api/openapi.yaml`. После его изменения
нужно выполнить `task generate-server`.

## Обработка аватарки

1. API проверяет JPEG, PNG или WebP размером до 10 МБ.
2. Исходный файл сохраняется в MinIO, метаданные — в PostgreSQL.
3. API публикует событие `avatar.uploaded` в RabbitMQ.
4. Worker создаёт миниатюры и обновляет статус записи.
5. Удаление помечает запись удалённой и публикует `avatar.deleted` для очистки файлов.

## Развёртывание в Kubernetes

Helm chart расположен в `deploy/helm/gophprofile`. Он создаёт:

- Deployment и HPA для API и worker;
- Service, Ingress, ConfigMap и ссылку на заранее созданный Secret;
- liveness/readiness probes и ServiceMonitor;
- ServiceAccount с нулевыми API-правами, restricted SecurityContext и NetworkPolicy;
- Helm hook Job для миграций PostgreSQL.

Chart не устанавливает PostgreSQL, RabbitMQ, MinIO, Ingress Controller,
metrics-server, Prometheus Operator и Jaeger. Эти компоненты должны уже работать
в кластере. `ServiceMonitor` требует CRD Prometheus Operator, а HPA — metrics-server.

### Локальный Kubernetes

Скопируйте пример values в локальный файл, создайте Secret, затем соберите
образ и загрузите его в используемый локальный кластер:

```bash
cp deploy/helm/gophprofile/values-dev.example.yaml \
  deploy/helm/gophprofile/values-dev.yaml

kubectl create namespace gophprofile
kubectl -n gophprofile create secret generic gophprofile-secrets \
  --from-literal=DATABASE_DSN='<DATABASE_DSN>' \
  --from-literal=AMQP_URL='<AMQP_URL>' \
  --from-literal=S3_ACCESS_KEY='<S3_ACCESS_KEY>' \
  --from-literal=S3_SECRET_KEY='<S3_SECRET_KEY>'

docker build -t gophprofile:latest .
helm lint deploy/helm/gophprofile -f deploy/helm/gophprofile/values-dev.yaml
helm upgrade --install gophprofile deploy/helm/gophprofile \
  --namespace gophprofile \
  -f deploy/helm/gophprofile/values-dev.yaml
```

`values-dev.example.yaml` не содержит credentials. Локальный `values-dev.yaml`
игнорируется Git и Helm package. HPA, ServiceMonitor и NetworkPolicy в dev-примере
отключены для простого локального кластера.

### Production

Сначала создайте namespace и Secret. Значения ниже нужно заменить реальными:

```bash
kubectl create namespace gophprofile
kubectl label namespace gophprofile \
  pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted

kubectl -n gophprofile create secret generic gophprofile-secrets \
  --from-literal=DATABASE_DSN='postgres://USER:PASSWORD@postgres.gophprofile.svc.cluster.local:5432/gophprofile?sslmode=require' \
  --from-literal=AMQP_URL='amqps://USER:PASSWORD@rabbitmq.gophprofile.svc.cluster.local:5671/' \
  --from-literal=S3_ACCESS_KEY='CHANGE_ME' \
  --from-literal=S3_SECRET_KEY='CHANGE_ME'
```

Обновите host, TLS Secret, адреса зависимостей и image tag в
`values-prod.yaml`, затем установите релиз:

```bash
helm lint deploy/helm/gophprofile -f deploy/helm/gophprofile/values-prod.yaml
helm upgrade --install gophprofile deploy/helm/gophprofile \
  --namespace gophprofile \
  -f deploy/helm/gophprofile/values-prod.yaml \
  --set image.tag=1.0.0 \
  --atomic \
  --timeout 10m
```

Если PostgreSQL, RabbitMQ, MinIO или OTLP collector находятся в других
namespace или вне кластера, добавьте разрешения в
`networkPolicy.extraEgress`. После деплоя:

```bash
kubectl -n gophprofile get deployments,pods,services,ingress,hpa
kubectl -n gophprofile rollout status deployment/gophprofile-server
kubectl -n gophprofile rollout status deployment/gophprofile-worker
```

Миграции выполняются hook Job до установки или обновления workload-ов.
API и worker сами миграции не запускают, поэтому несколько реплик стартуют
без гонок.

## Graceful shutdown

При `SIGTERM` API перестаёт принимать новые запросы и ждёт завершения текущих
в пределах `SHUTDOWN_PERIOD`. Worker отменяет consumer RabbitMQ и корректно
останавливает HTTP-сервер метрик. В Kubernetes процессам даётся 30 секунд через
`terminationGracePeriodSeconds`.

## Наблюдаемость

HTTP-запросы, операции PostgreSQL и MinIO, публикация RabbitMQ и обработка
сообщений worker входят в единый distributed trace. W3C `traceparent`
передаётся через RabbitMQ.

Prometheus собирает метрики API и worker с `/metrics`. В Docker Compose он
использует статическую конфигурацию, в Kubernetes оба Service автоматически
обнаруживаются через один ServiceMonitor. Grafana загружает дашборд
`GophProfile Overview`.

JSON-логи содержат `service`, `trace_id` и `span_id`. В локальном Compose
Fluent Bit отправляет их в OpenSearch с индексом `gophprofile-logs-*`.

## Архитектура Kubernetes

```mermaid
flowchart LR
    Client["Клиент"] --> Ingress["Ingress"]
    Ingress --> APIService["Service API"]
    APIService --> API["API Deployment + HPA"]

    Config["ConfigMap + Secret"] --> API
    Config --> Worker["Worker Deployment + HPA"]

    API --> DB[("PostgreSQL")]
    API --> S3[("MinIO / S3")]
    API --> MQ[("RabbitMQ")]
    MQ --> Worker
    Worker --> DB
    Worker --> S3

    Migration["Helm hook: migrate Job"] --> DB
    Monitor["ServiceMonitor"] --> APIService
    Monitor --> WorkerService["Worker metrics Service"]
    Prometheus["Prometheus Operator"] --> Monitor
```

NetworkPolicy разрешает API-трафик только от namespace Ingress Controller,
метрики — от monitoring namespace, а исходящие соединения — к DNS и портам
PostgreSQL, RabbitMQ, MinIO и OTLP. Pod Security обеспечивается без устаревшего
PodSecurityPolicy: контейнеры работают от UID `10001`, без privilege escalation,
с `RuntimeDefault` seccomp, read-only root filesystem и удалёнными capabilities.
