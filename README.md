# Quota +

`quota-plus`는 CLIProxyAPI에 등록된 OpenCode Go, Ollama Cloud, CommandCode GOAT 키의 할당량을 관리 API로 제공합니다.

## 호스트 버전과 검증 상태

빌드에 사용하는 SDK는 CLIProxyAPI **v8.0.20**입니다. 설치 대상은 v8.0.20 이상으로 정하지만, 실제 호스트에서 검증한 지원 버전은 아직 없습니다.
릴리스 전에 아래 수동 검증을 실행하고, 확인한 호스트 버전을 이 절에 기록해야 합니다. v7.3.4는 지원 대상으로 안내하지 않습니다.

세 제공자의 실제 키 조회, `quota.describe` 중 `host.auth.list` 콜백, 새 키 추가 후 `SupportedProviders` 갱신 시점은 아직 검증하지 않았습니다.

## 설치

다음 절차는 릴리스 에셋이 게시된 뒤 사용합니다. 현재 체크아웃에는 CI와 릴리스 워크플로가 포함되어 있지 않습니다.

1. [GitHub Releases](https://github.com/dungsil/cpa-plugin-quota-plus/releases)에서 같은 버전의 플랫폼별 zip과 `checksums.txt`를 내려받습니다. 파일 이름의 `<버전>`에는 태그의 `v`를 넣지 않습니다.

   | 플랫폼 | zip 파일 | zip 루트의 라이브러리 |
   | --- | --- | --- |
   | Linux amd64 | `quota-plus_<버전>_linux_amd64.zip` | `quota-plus.so` |
   | Linux arm64 | `quota-plus_<버전>_linux_arm64.zip` | `quota-plus.so` |
   | macOS arm64 | `quota-plus_<버전>_darwin_arm64.zip` | `quota-plus.dylib` |
   | Windows amd64 | `quota-plus_<버전>_windows_amd64.zip` | `quota-plus.dll` |

2. 내려받은 zip의 SHA-256을 계산하고, `checksums.txt`에서 **같은 파일 이름**의 값과 비교합니다. 값이 다르면 설치하지 않습니다.

   ```sh
   # Linux
   sha256sum quota-plus_<버전>_linux_amd64.zip
   # macOS
   shasum -a 256 quota-plus_<버전>_darwin_arm64.zip
   ```

   ```powershell
   # Windows
   Get-FileHash -Algorithm SHA256 -LiteralPath 'quota-plus_<버전>_windows_amd64.zip'
   ```

   `<버전>`과 플랫폼은 실제 파일 이름으로 바꿉니다. SHA-256의 영문 대소문자 차이는 무시합니다.

3. zip을 풀고 라이브러리 하나를 호스트의 `plugins/`에 둡니다. 플랫폼별로 분리하려면 `plugins/<GOOS>/<GOARCH>/`에 둡니다. Windows amd64의 예는 `plugins/windows/amd64/quota-plus.dll`입니다.
4. 기존 `config.yaml`의 `plugins` 항목에 아래 설정을 반영합니다. 기존 플러그인 설정은 유지합니다.

   ```yaml
   plugins:
     enabled: true
     dir: "plugins"
     configs:
       quota-plus:
         enabled: true
   ```

5. 호스트를 시작하고 아래 관리 API 조회 절차로 설치를 확인합니다. 플러그인 전용 설정이나 별도 API 키 입력은 필요하지 않습니다.

Intel macOS 바이너리는 제공 대상이 아닙니다. CLIProxyAPI-Plugins-Store에는 등록하지 않습니다.

## 대상 키

호스트의 `openai-compatibility` 등에 등록한 키에서 base URL이 다음 공식 URL 중 하나와 일치해야 합니다.

- OpenCode Go: `https://opencode.ai/zen/go/v1`, `https://opencode.ai/zen/go`
- Ollama Cloud: `https://ollama.com/api`, `https://ollama.com/v1`
- CommandCode GOAT: `https://api.commandcode.ai/provider/v1`

호스트 이름의 대소문자와 끝의 `/` 하나는 무시합니다. 경로는 정확히 일치해야 합니다.
`http` 주소, OpenCode Zen 주소, 로컬 Ollama 주소, 사용자 프록시 주소는 대상이 아닙니다. 쿼리 문자열이나 추가 경로가 있는 주소도 대상이 아닙니다.
`openai-compatibility` 항목의 이름을 제공자 이름으로 바꿀 필요는 없습니다.

## 관리 API로 조회

현재 관리 UI는 플러그인 할당량을 표시하지 않습니다. 관리 API로 조회합니다.
관리 API 인증에는 **호스트의 관리 키**를 사용합니다. 제공자의 API 키를 요청 본문에 넣지 않습니다.

아래 Bash 예시의 관리 주소와 가상 값을 실제 로컬 환경에 맞게 바꿉니다. 예시에는 실제 키나 실제 응답이 없습니다.
`MANAGEMENT_KEY`에는 `remote-management.secret-key`에 설정한 키의 원문을 사용하며, 해시값은 사용하지 않습니다.

```bash
MANAGEMENT_URL='http://127.0.0.1:8317'
read -r -s -p 'Management key: ' MANAGEMENT_KEY
printf '\n'

curl --silent --show-error --fail-with-body \
  -H "Authorization: Bearer ${MANAGEMENT_KEY}" \
  "${MANAGEMENT_URL}/v0/management/auth-files"
```

반환된 `files`에서 대상 항목의 `supports_quota: true`를 확인하고, 그 항목의 `auth_index`를 복사합니다.
아래 `EXAMPLE_AUTH_INDEX`를 복사한 값으로 바꿉니다.

```bash
curl --silent --show-error --fail-with-body \
  -H "Authorization: Bearer ${MANAGEMENT_KEY}" \
  -H 'Content-Type: application/json' \
  --data '{"auth_index":"EXAMPLE_AUTH_INDEX"}' \
  "${MANAGEMENT_URL}/v0/management/quota/fetch"

unset MANAGEMENT_KEY
```

기간 창의 남은 비율은 0에서 1 사이입니다. 제공자가 초기화 시각을 주면 UTC RFC3339 형식으로 반환합니다.
OpenCode Go는 `5h`, `weekly`, `monthly`를 제공합니다. CommandCode는 `5h`, `weekly`와 크레딧 정보를 제공합니다.
Ollama Cloud는 상류 응답에 있는 기간 창만 반환합니다. 레거시 요금제에는 `5h`와 `weekly`가 포함될 수 있습니다.

## 알려진 제약

- 세 제공자의 사용량 API는 모두 **문서화되지 않은 상류 API**입니다. 응답 형식이 예고 없이 바뀔 수 있습니다. 예상한 형식과 다르면 `unexpected response format` 오류가 발생합니다.
- `claude-api-key`처럼 고정 provider 키를 쓰는 항목에 공식 URL을 등록하면, 같은 provider 키의 모든 할당량 요청이 이 플러그인으로 전달될 수 있습니다. 공식 URL이 아닌 키는 조회 오류가 발생합니다.
- 같은 provider 키를 선언하는 다른 할당량 플러그인이 있으면 호스트가 먼저 찾은 플러그인만 사용합니다.
- Ollama Cloud는 초기화 시각과 요금제 이름을 제공하지 않습니다. 빈 `resetTime`을 반환하며 시각을 추정하지 않습니다.
- `quota.reset`은 지원하지 않습니다. 상류 호출은 호스트의 request-log에 기록되지 않습니다.
- 조회 결과를 캐시하거나 상류 호출을 재시도하지 않습니다.
- 새 키를 추가했을 때 `SupportedProviders`와 `supports_quota`가 재시작 없이 갱신되는지는 아직 확인하지 않았습니다. 아래 4단계에서 결과와 재시작 필요 여부를 기록합니다.

## 릴리스 전 수동 검증

실제 키와 실제 CLIProxyAPI 호스트를 사용하여 릴리스 담당자가 실행합니다. 키는 로컬 설정에만 저장합니다.
실제 키, 인증 헤더, 실제 응답은 저장소나 검증 기록에 남기지 않습니다.

1. CLIProxyAPI v8.0.20 이상의 정확한 버전을 확인합니다. 해당 플랫폼용 `quota-plus` 라이브러리를 설치하고 플러그인을 켠 호스트를 로컬에서 실행합니다.
2. `openai-compatibility`에 세 제공자의 공식 URL과 실제 키를 등록합니다. 각 제공자를 구분할 수 있는 이름을 사용합니다.
3. 위 관리 API 예시로 세 제공자의 `supports_quota: true`와 `auth_index`를 확인합니다. 각 키의 조회가 성공하는지 확인합니다. 플러그인 등록과 조회가 중단되지 않는지 확인하여 `quota.describe` 중 `host.auth.list` 콜백도 검증합니다.
4. 호스트가 실행 중인 상태에서 `config.yaml`에 **새 이름의** 항목과 키를 추가합니다. 설정이 다시 적용된 뒤 재시작 없이 3단계를 반복합니다. 새 provider 키의 `supports_quota` 반영 여부를 확인합니다. 반영되지 않으면 재시작 후 다시 확인하고 제약을 기록합니다.
5. 별도 테스트 항목에 잘못된 제공자 키를 등록하고 조회합니다. 올바른 관리 키로 인증한 상태에서 플러그인 오류 메시지에 `unauthorized`가 있는지 확인합니다. 관리 API 자체의 인증 실패와 구분합니다. 검증 뒤 테스트 항목을 제거합니다.

검증을 마친 담당자는 아래 항목을 갱신하고, 이 문서 상단에 검증한 지원 호스트 버전을 적습니다.

| 검증 항목 | 현재 결과 |
| --- | --- |
| 정확한 호스트 버전과 플랫폼 | 미검증 |
| 세 제공자의 실제 키 조회 | 미검증 |
| 잘못된 제공자 키의 `unauthorized` | 미검증 |
| `quota.describe` 중 `host.auth.list` 콜백 | 미검증 |
| 새 provider 키의 재시작 전 `supports_quota` 반영 | 미검증 |
| 반영되지 않을 때 재시작 후 결과 | 미검증 |

## License
This project under the [MIT License](./LICENSE).
