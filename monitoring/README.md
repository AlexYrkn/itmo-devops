# Стек мониторинга для лаб >= 3

Это тот же стек, что и во второй лабе (Prometheus, Alertmanager, Grafana, Loki, Alloy, Karma), только Prometheus и Alertmanager теперь ставятся через `kube-prometheus-stack`, то есть с Prometheus Operator.

Переделать пришлось потому, что в лабах >= 3 метрики и алерты надо класть в чарт сервиса как `ServiceMonitor` и `PrometheusRule`, а это ресурсы оператора.  

Loki, Alloy и Karma взяты из `lab2/` почти без изменений, папку второй лабы не трогал. UI открываются через NodePort: Prometheus на `localhost:9090`, Alertmanager на `:9093`, Grafana на `:3000` (`admin` / `lab2admin`).


```bash
kubectl create namespace monitoring
helm install kps prometheus-community/kube-prometheus-stack -n monitoring -f monitoring/kube-prometheus-stack.yaml
helm install loki  grafana/loki   -n monitoring -f lab2/helm/values/loki.yaml
helm install alloy grafana/alloy  -n monitoring -f lab2/helm/values/alloy.yaml
helm install karma wiremind/karma -n monitoring -f lab2/helm/values/karma.yaml -f monitoring/karma.yaml
```
