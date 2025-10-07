### IP Address Collection and Management Service with multiple formats 
For english readme: 
[README.en.md](README.en.md)
Demo URL: 
[https://iplist.opencck.org](https://iplist.opencck.org)
![iplist](https://github.com/user-attachments/assets/e004bc06-3646-4eec-acce-9c6799a3661a)
# Сервис сбора IP-адресов и CIDR зон
Данный сервис предназначен для сбора и обновления IP-адресов (IPv4 и IPv6), а также их CIDR зон для указанных доменов.
Это асинхронный PHP веб-сервер на основе 
[AMPHP](https://amphp.org/) и Linux-утилит 
`whois` и `ipcalc`.
Сервис предоставляет интерфейсы для получения списков зон ip адресов указанных доменов (IPv4 адресов, IPv6 адресов, а также CIDRv4 и CIDRv6 зон) в различных форматах, включая текстовый, JSON, форматы скриптов для добавления в "Address List" на роутерах Mikrotik (RouterOS), Keenetic KVAS\BAT, SwitchyOmega, Amnezia и др.
Основные возможности
- Сбор и авитоматическое обновление IP-адресов и CIDR зон для доменов.
- Поддержка вывода данных в различных форматах (JSON, lst, Mikrotik, OpenWRT, ipset и т.д).
- Интеграция с внешними источниками данных (поддержка импорта начальных данных из внешних URL).
- Легкое развертывание с помощью Docker Compose.
- Настройка через JSON файлы для управления доменами.
Используемые технологии
- PHP 8.1+ (amphp, revolt)
- whois, ipcalc (linux)
![Image](https://github.com/user-attachments/assets/3579c35b-3d81-4a40-94d8-15a0807b44c6)
# Форматы выгрузки
| формат   | описание                      |
|----------|-------------------------------|
| json     | JSON формат                   |
| text     | Разделение новой строкой      |
| comma    | Разделение запятыми           |
| geoip    | v2rayGeoIPDat                 |
| mikrotik | MikroTik Script               |
| switchy  | SwitchyOmega RuleList         |
| nfset    | Dnsmasq nfset                 |
| ipset    | Dnsmasq ipset                 |
| clashx   | ClashX                        |
| kvas     | Keenetic KVAS                 |
| bat      | Keenetic Routes .bat          |
| amnezia  | Amnezia filter list           |
| pac      | Proxy Auto-Configuration file |
## Настройки
Конфигурационные файлы хранятся в `config/<группа>/<портал>.json`. Каждый JSON файл представляет собой конфигурацию для конкретного портала, задавая домены для мониторинга и источники начальных данных по IP и CIDR.
```json
{
    "domains": [
        "youtube.com",
        "www.youtube.com",
        "m.youtube.com",
        "www.m.youtube.com",
        "googlevideo.com",
        "www.googlevideo.com",
        "ytimg.com",
        "i.ytimg.com"
    ],
    "dns": ["127.0.0.11:53", "77.88.8.88:53", "8.8.8.8:53"],
    "timeout": 43200,
    "ip4": [],
    "ip6": [],
    "cidr4": [],
    "cidr6": [],
    "external": {
        "domains": ["https://raw.githubusercontent.com/nickspaargaren/no-google/master/categories/youtubeparsed"],
        "ip4": ["https://raw.githubusercontent.com/touhidurrr/iplist-youtube/main/ipv4_list.txt"],
        "ip6": ["https://raw.githubusercontent.com/touhidurrr/iplist-youtube/main/ipv6_list.txt"],
        "cidr4": ["https://raw.githubusercontent.com/touhidurrr/iplist-youtube/main/cidr4.txt"],
        "cidr6": ["https://raw.githubusercontent.com/touhidurrr/iplist-youtube/main/cidr6.txt"]
    }
}
```
| свойство | тип      | описание                                                                                                                                                                                                             |
|----------|----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| domains  | string[] | Список доменов портала                                                                                                                                                                                               |
