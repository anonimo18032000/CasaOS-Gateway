# CasaOS-Gateway

[![Go Reference](https://pkg.go.dev/badge/github.com/IceWhaleTech/CasaOS-Gateway.svg)](https://pkg.go.dev/github.com/IceWhaleTech/CasaOS-Gateway) [![Go Report Card](https://goreportcard.com/badge/github.com/IceWhaleTech/CasaOS-Gateway)](https://goreportcard.com/report/github.com/IceWhaleTech/CasaOS-Gateway) [![goreleaser](https://github.com/IceWhaleTech/CasaOS-Gateway/actions/workflows/release.yml/badge.svg)](https://github.com/IceWhaleTech/CasaOS-Gateway/actions/workflows/release.yml) [![codecov](https://codecov.io/gh/IceWhaleTech/CasaOS-Gateway/branch/main/graph/badge.svg?token=5JIHXF1RJ4)](https://codecov.io/gh/IceWhaleTech/CasaOS-Gateway)

O CasaOS Gateway é um serviço de gateway de API dinâmico que pode ser usado para expor APIs de diversos outros serviços baseados em HTTP.

Este serviço de gateway vem com uma API de gerenciamento simples para que outros serviços registrem suas APIs por caminhos de rota. Uma requisição HTTP que chegar na porta do gateway será encaminhada para o serviço registrado naquele caminho de rota.

> Como boa prática, um serviço atrás deste gateway deve se vincular APENAS ao localhost (`127.0.0.1` para IPv4, `::1` para IPv6), de forma que nenhum acesso externo pela rede seja permitido.

## Configuração

Ao iniciar, ele vai procurar pelo arquivo `gateway.ini` na seguinte ordem:

```bash
./gateway.ini
./conf/gateway.ini
$HOME/.casaos/gateway.ini
/etc/casaos/gateway.ini
```

Veja [gateway.ini.sample](./build/etc/casaos/gateway.ini.sample) para a configuração padrão.

## Execução

Uma vez em execução, o endereço do gateway e o endereço de gerenciamento estarão disponíveis nos arquivos dentro do `RuntimePath` especificado na configuração.

```bash
$ cat /var/run/casaos/gateway.url 
[::]:8080 # a porta é especificada na configuração

$ cat /var/run/casaos/management.url 
[::]:34703 # a porta é atribuída aleatoriamente
```

## Exemplo

Supondo que

- a API de gerenciamento está rodando na porta `34703`
- o gateway está rodando na porta `8080`
- alguma API rodando em `http://localhost:12345/ping` que simplesmente retorna `pong`.

Registre a API da seguinte forma:

- POST `http://localhost:34703/v1/gateway/routes`

  ```json
  {
          "path": "/ping",
          "target": "http://localhost:12345"
  }
  ```

  ou pela linha de comando:

  ```bash
  $ curl 'localhost:34703/v1/gateway/routes' --data-raw '
      {"path": "/ping", "target": "http://localhost:12345"}
    '
  ```

Agora execute

```bash
$ curl localhost:8080/ping
{"message":"pong"}
```

... o que é equivalente a

```bash
$ curl localhost:12345/ping
{"message":"pong"}
```
