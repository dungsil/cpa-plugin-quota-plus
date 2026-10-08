# 할당량 플러그인 (Quota Plugin)

CLIProxyAPI에 등록된 API 키의 할당량을 제공자 서버에서 읽어 오는 플러그인의 용어를 정리합니다. 호스트가 쓰는 낱말과 이 플러그인이 쓰는 낱말이 비슷해서 서로 구분해야 합니다.

## 용어

**제공자 (Provider)**:
API 키를 발급하고 할당량을 관리하는 외부 서비스입니다. 지금은 OpenCode Go, Ollama Cloud, CommandCode 세 곳입니다.
_피할 표현_: 공급자, 벤더, 업체

**provider 키 (Provider key)**:
호스트가 자격 증명마다 붙이는 provider 이름 문자열입니다. 예를 들어 `openai-compatible-opencode-go`나 `claude`입니다. 제공자 (Provider)와 같은 뜻이 아닙니다.
_피할 표현_: provider 이름, 제공자 이름

**공식 URL (Official URL)**:
제공자 (Provider)가 추론 요청용으로 공개한 base URL입니다. 이 플러그인은 공식 URL로 키가 어느 제공자의 것인지 정합니다.
_피할 표현_: 기본 URL, 엔드포인트

**사용량 API (Usage API)**:
제공자 서버가 API 키 하나의 사용 비율, 초기화 시각, 크레딧이나 비용을 돌려주는 API입니다.
_피할 표현_: 할당량 API, quota 엔드포인트

**문서화되지 않은 상류 API (Undocumented upstream API)**:
제공자 서버가 실제로 제공하지만 공개 API 문서에 없는 API입니다. 응답 형식이 예고 없이 바뀔 수 있습니다.
_피할 표현_: 비공식 API, 비공개 API

**기간 창 (Window)**:
할당량이 다시 채워지는 기간 하나입니다. 이름은 `5h`, `weekly`, `monthly` 세 가지입니다.
_피할 표현_: 윈도우, 주기, 리셋 주기

**남은 비율 (Remaining fraction)**:
기간 창 (Window) 하나에서 아직 쓰지 않은 할당량의 비율입니다. 0과 1 사이의 수입니다.
_피할 표현_: 잔여량, 남은 퍼센트
