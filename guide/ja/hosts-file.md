# ホストファイル

← [README](../../README.ja.md)

## hosts.yaml の例

`hosts:` キーでホストを列挙します。CLI で明示的に指定したオプションは YAML の値より優先されます。

```yaml
hosts:
  - google.com
  - 1.1.1.1
interval: 500
timeout: 2000
mtr: true
asn: true
port:
  - 443/tcp
  - 53/udp
dns-server: 8.8.8.8
resolve-all: true
web: true
web-port: 8080
thresholds:
  rtt-warn: 50      # ミリ秒 (オレンジ)
  rtt-crit: 200     # ミリ秒 (赤)
  jitter-warn: 10   # ミリ秒 (オレンジ)
  jitter-crit: 50   # ミリ秒 (赤)
  loss-warn: 20     # パーセント (オレンジ)
  loss-crit: 80     # パーセント (赤)
```

## YAML でのホストグループ定義

`groups:` キーで名前付きグループを定義できます。グループはヘッダ行と最悪値集計行つきで表示されます。`hosts:` に列挙したホストと共存でき、グループ外ホストは表の上部に表示されます。

```yaml
hosts:
  - 8.8.8.8        # グループ外 — 全グループより上に表示

groups:
  - name: US DNS
    hosts:
      - 1.1.1.1
      - 8.8.4.4
  - name: Japan
    hosts:
      - dns.google
      - dns.cloudflare.com
```

実行可能なサンプルは [examples/hosts-groups.yaml](../../examples/hosts-groups.yaml) を参照（`mping -f examples/hosts-groups.yaml`）。

## 大量のホストを追加する: パターン・表示名・include

ホストの指定（`hosts:`・`groups[].hosts:`）には、複数ホストに展開されるパターンを書けます。

| パターン | 展開結果 |
|---|---|
| `10.0.0.1-20` | `10.0.0.1` 〜 `10.0.0.20`（最終オクテットの範囲） |
| `10.0.0.250-10.0.1.5` | 範囲内のすべての IPv4 アドレス |
| `192.0.2.0/28` | `192.0.2.1` 〜 `192.0.2.14`（IPv4 の /30 以上はネットワーク・ブロードキャストアドレスを除外。IPv6 の CIDR は全アドレス） |
| `core-sw{01..12}.lab` | `core-sw01.lab` 〜 `core-sw12.lab`（ゼロ埋めを維持。`{N..M}` を複数書くと全組み合わせ） |

1パターンの展開は最大 1024 ホスト、hosts ファイル全体（include を含む）は最大 4096 ターゲットです。IPv6 の範囲指定は非対応のため、CIDR で指定してください。

`name` を付けると、アドレスの代わりにその名前を表示します（IP は名前の横に表示されます）。名前は重複不可で、他のエントリのホストと同じ文字列にもできません。複数ホストに展開されるパターンには名前を付けられません。レポートでも、ホストの代わりに名前が記録されます（解決した IP は引き続き記録されます）。

```yaml
hosts:
  - {host: 10.0.0.1, name: core-sw01}
```

`include:`（トップレベルまたはグループごと）で、NetBox の IP Addresses CSV エクスポート（`IP Address` 列を含む CSV）からホストを追加できます。パスは YAML ファイルからの相対パスです。エクスポート1つだけなら `mping -f netbox.csv` のように `-f` で直接指定することもできます。単純なホスト一覧（1行1ホストや `host,name` 形式の CSV）には対応しておらず、エラーになります。そうしたホストは YAML ファイルに書いてください。プレフィックス長（`/23` など）は自動的に除去され、`Status` が `Active`（または空）の行が対象になります。表示名には `DNS name`（空なら `Parent`）が使用され、同名になる場合は `Interface` や IP アドレスを付与して自動的に一意化されます。ファイルは mping を実行するユーザーが読める必要があります。include ファイルも YAML と同様に変更が監視され、自動で反映されます（まだ存在しないファイルを YAML に追加した場合も、作成した時点で反映されます）。

```yaml
groups:
  - name: Core
    hosts: ["core-sw{01..04}.lab"]
  - name: Branches
    include: [tokyo-netbox.csv, osaka-netbox.csv]
```

サンプルは [examples/hosts-patterns.yaml](../../examples/hosts-patterns.yaml) と [examples/branches.csv](../../examples/branches.csv) を参照。
