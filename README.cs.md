# oci-patch

**Nástroj pro přenos a úpravu OCI image — bez Docker daemona.**

`oci-patch` kopíruje container image mezi registry (podobně jako `skopeo`) a navíc při přenosu aplikuje deklarativní *recipe*, které image upravují — přidávají soubory, přepisují metadata, mění base image a podobně. Vše běží jako jediná statická binárka bez jakýchkoli runtime závislostí.

---

## Obsah

- [Proč oci-patch](#proč-oci-patch)
- [Srovnání se skopeo](#srovnání-se-skopeo)
- [Instalace](#instalace)
- [Příkazy](#příkazy)
  - [copy](#copy)
  - [inspect](#inspect)
  - [cat](#cat)
  - [ls](#ls)
  - [tree](#tree)
- [Credentials](#credentials)
- [Recipe](#recipe)
  - [Struktura recipe](#struktura-recipe)
  - [Přehled akcí](#přehled-akcí)
  - [Podmínky (conditions)](#podmínky-conditions)
  - [Multi-source recipes](#multi-source-recipes)
- [Příklady](#příklady)
- [Spuštění testů](#spuštění-testů)

---

## Proč oci-patch

Typický postup pro úpravu container image dnes vypadá takto:

```
docker pull image
docker run --entrypoint ... image   # vykuchání souboru
docker build -f Dockerfile.patch .  # sestavení upraveného image
docker push patched-image
```

To vyžaduje běžící Docker daemon, Dockerfile a mezikontejnery. `oci-patch` celý tento postup nahradí jediným příkazem s YAML recipe — žádný daemon, žádný Dockerfile, žádné mezikontejnery, žádná binárka `docker`.

---

## Srovnání se skopeo

`oci-patch` je **záměrně CLI-kompatibilní** se `skopeo` pro příkazy, které implementuje (`copy`, `inspect`, `--version`). Stačí nahradit binárku `skopeo` za `oci-patch` — stávající skripty fungují beze změny argumentů.

| Funkce | skopeo | oci-patch |
|--------|--------|-----------|
| Kopie image mezi registry | ✅ | ✅ |
| Inspect metadat image (JSON) | ✅ | ✅ |
| Výpis souboru z image | ❌ | ✅ |
| Výpis tagů repozitáře | ✅ | ✅ (automaticky při inspect) |
| Bez Docker daemona | ✅ | ✅ |
| Auth přes `~/.docker/config.json` | ✅ | ✅ |
| `--src-creds` / `--dest-creds` | ✅ | ✅ |
| `--override-os` / `--override-arch` | ✅ | ✅ |
| `--dest-no-reuse` fallback | ✅ | ✅ |
| `--debug` výstup | ✅ | ✅ |
| **Injekce souborů do image** | ❌ | ✅ |
| **Přepis ENTRYPOINT / CMD** | ❌ | ✅ |
| **Nastavení ENV / LABEL / USER / WORKDIR** | ❌ | ✅ |
| **Mazání souborů (whiteout vrstva)** | ❌ | ✅ |
| **Kopie souborů mezi image** | ❌ | ✅ |
| **Změna base image** | ❌ | ✅ |
| **Extrakce souborů z image na disk** | ❌ | ✅ |
| **Rozbalení archivů uvnitř image** | ❌ | ✅ |
| **Výpis adresáře v image** | ❌ | ✅ |
| **Výpis stromu cesty v image** | ❌ | ✅ |
| **Deklarativní YAML recipe** | ❌ | ✅ |
| **Více recipe na jeden copy** | ❌ | ✅ |
| **Podmíněné kroky (if-not-exists)** | ❌ | ✅ |
| **Multi-source kompozice image** | ❌ | ✅ |
| Spouštění příkazů uvnitř kontejneru (`RUN`) | ✅ | ❌ záměrně vynecháno |
| Ověření podpisů (sigstore) | ✅ | ❌ neimplementováno |
| OCI image layout (adresář) | ✅ | ❌ neimplementováno |

### Klíčové rozdíly podrobně

**1. Úprava image při přenosu**

`skopeo copy` je čistě přenosový nástroj — přesune data z A do B. `oci-patch copy` image při průchodu modifikuje: stáhne vrstvy, aplikuje kroky z recipe (každý krok přidá novou OCI vrstvu nebo přepíše config image), výsledek pushne. Původní image zůstane nedotčen.

**2. Žádný `RUN` — záměrně**

Spouštění příkazů uvnitř kontejneru při buildu (jako `apk upgrade`, `adduser`) vyžaduje container runtime (runc, crun nebo buildkit). `oci-patch` tuto funkcionalitu záměrně vynechává, aby zůstal statickou binárkou bez závislostí. Vše co dělá je čistá manipulace s vrstvami a configem image v paměti.

**3. Multi-source kompozice**

Recipe může deklarovat více pojmenovaných zdrojových image. Kroky mohou ze kteréhokoli z nich extrahovat soubory a skládat je do nového image — podobně jako Docker multi-stage builds, ale deklarativně a bez daemona.

**4. Podmíněné kroky**

Krok může být podmíněn pomocí `condition: if-not-exists: /cesta` — pokud soubor v aktuálním stavu image již existuje, krok se přeskočí. To umožňuje idempotentní patchování: nástroj se injektuje jen tehdy, pokud ještě není přítomen.

**5. CLI kompatibilita se skopeo**

Všechny flagy používané v nejběžnějších skopeo workflow jsou podporovány se stejnými názvy:

```bash
# skopeo příkazy fungují identicky jako oci-patch
skopeo inspect --creds user:pass docker://registry.example.com/app:latest
skopeo copy --src-creds u:p --dest-creds u:p docker://src/img:tag docker://dst/img:tag
skopeo copy --debug --dest-no-reuse docker://src docker://dst
skopeo --version
```

---

## Instalace

```bash
git clone https://github.com/datalite/oci-patch
cd oci-patch
go build -o oci-patch .
```

Cross-kompilace pro Linux amd64:

```bash
GOOS=linux GOARCH=amd64 go build -o oci-patch-linux-amd64 .
```

---

## Příkazy

### copy

```
oci-patch copy [flagy] ZDROJ CÍL
```

Zkopíruje image ze ZDROJE do CÍLE a v pořadí aplikuje všechny zadané recipe.

**Flagy:**

| Flag | Popis |
|------|-------|
| `--src-creds user:pass` | Credentials pro zdrojový registry |
| `--dest-creds user:pass` | Credentials pro cílový registry |
| `--recipe cesta/k/recipe.yml` | Recipe soubor (lze opakovat) |
| `--debug` | Zapne podrobný výstup |
| `--batch-file cesta/k/images.yml` | Spustí více copy jobů ze YAML souboru |
| `--continue-on-error` | V batch režimu pokračuje další položkou po selhání |
| `--dest-no-reuse` | Zakáže cross-repository blob mount/reuse v cíli (fallback při problémech) |
| `--reduce` | Materializuje finální filesystem do jediné minimální vrstvy |
| `--reduce-changes` | Zachová base vrstvy a změny z recipe sloučí do jedné delta vrstvy |
| `--progress` | Vykreslí terminálové progress bary |
| `--progress-json` | Emituje machine-readable progress eventy jako JSONL |

**Příklady:**

```bash
# Jednoduchá kopie bez recipe
oci-patch copy docker://registry.example.com/app:1.0 docker://registry.example.com/app:1.0-copy

# Kopie s credentials
oci-patch copy \
  --src-creds srcuser:srcpass \
  --dest-creds dstuser:dstpass \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Kopie s recipe (injekce nástroje)
oci-patch copy \
  --src-creds user:pass \
  --dest-creds user:pass \
  --recipe inject-tool.yml \
  docker://registry.example.com/app:latest \
  docker://registry.example.com/app:patched

# Více recipe aplikovaných v pořadí
oci-patch copy \
  --recipe recipe-1.yml \
  --recipe recipe-2.yml \
  docker://src/app:tag \
  docker://dst/app:tag

# Zdroj podle digestu (SHA256)
oci-patch copy \
  --src-creds user:pass \
  docker://registry.example.com/app@sha256:abc123... \
  docker://registry.example.com/app:stable

# Fallback s --dest-no-reuse při chybě blob reuse
oci-patch copy --debug --dest-no-reuse \
  --src-creds user:pass --dest-creds user:pass \
  docker://src/app:tag docker://dst/app:tag

# Machine-readable progress pro integrace
oci-patch copy --progress-json \
  docker://src/app:tag docker://dst/app:tag
```

### Batch copy

Pro opakované kopírování více image můžeš použít `--batch-file`. Stejné CLI flagy se aplikují na všechny položky v souboru.

```yaml
# images.yml
copy:
  - src: harbor.cetin/tsm/release/tsm-config:TSM-Core_RE60_BF01_01.01
    dest: harbor.cetin/tsm/release/tsm-config:2.2-test-latest
  - src: harbor.cetin/tsm/release/tsm-ticket:TSM-Core_RE60_BF01_01.01
    dest: harbor.cetin/tsm/release/tsm-ticket:2.2-test-latest
```

```bash
oci-patch copy \
  --batch-file images.yml \
  --src-insecure \
  --dest-insecure \
  --progress
```

Batch běží sériově. Bez `--continue-on-error` se zastaví na první chybě. S `--continue-on-error` pokračuje dalšími položkami, ale pokud aspoň jedna položka selže, příkaz na konci vrátí nenulový exit code a vypíše souhrn selhání.

Primární klíče v batch souboru jsou `src` a `dest`, stejně jako CLI flagy `--src-*` a `--dest-*`. Jako aliasy jsou podporované i `source` a `destination`. Duplicitní cílové image jsou odmítnuté při validaci souboru.

### inspect

```
oci-patch inspect [flagy] IMAGE
```

Stáhne a zobrazí metadata image jako JSON (formát kompatibilní se skopeo).

**Flagy:**

| Flag | Popis |
|------|-------|
| `--creds user:pass` | Credentials pro registry |
| `--override-os linux` | Výběr OS z multi-platform indexu |
| `--override-arch arm64` | Výběr architektury |
| `--override-variant v8` | Výběr varianty |
| `--no-tags` | Přeskočí načítání tagů (rychlejší) |
| `--insecure` / `--src-insecure` | Přeskočí TLS ověření certifikátu zdrojového registry |

**Příklady:**

```bash
# Základní inspect
oci-patch inspect docker://docker.io/library/alpine:3.21

# S credentials
oci-patch inspect --creds user:pass docker://registry.example.com/app:latest

# Registry s nestandardním/self-signed certifikátem
oci-patch inspect --src-insecure docker://registry.example.com/app:latest

# Konkrétní platforma z multi-arch indexu
oci-patch inspect \
  --override-os linux \
  --override-arch arm64 \
  docker://docker.io/library/alpine:3.21

# Rychlý inspect bez načítání tagů
oci-patch inspect --no-tags docker://registry.example.com/app:latest
```

**Formát výstupu** (kompatibilní se skopeo):

```json
{
    "Name": "index.docker.io/library/alpine",
    "Digest": "sha256:f27cad9...",
    "RepoTags": ["3.19", "3.20", "3.21", "latest", "..."],
    "Created": "2026-04-16T23:53:15.704110414Z",
    "DockerVersion": "",
    "Labels": null,
    "Architecture": "amd64",
    "Os": "linux",
    "Layers": ["sha256:897d797d..."],
    "LayersData": [
        {
            "MIMEType": "application/vnd.oci.image.layer.v1.tar+gzip",
            "Digest": "sha256:897d797d...",
            "Size": 3646875,
            "Annotations": null
        }
    ],
    "Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
}
```

### cat

```
oci-patch cat [flagy] IMAGE CESTA
```

Stáhne image a vypíše jeden běžný soubor z filesystemu image na stdout.

Pokud soubor neexistuje, `cat` skončí úspěšně a nevypíše nic. S `--debug` příkaz navíc zaloguje warning, že soubor v image neexistuje.

**Flagy:**

| Flag | Popis |
|------|-------|
| `--creds user:pass` | Credentials pro registry |
| `--override-os linux` | Výběr OS z multi-platform indexu |
| `--override-arch arm64` | Výběr architektury |
| `--override-variant v8` | Výběr varianty |
| `--insecure` / `--src-insecure` | Přeskočí TLS ověření certifikátu zdrojového registry |
| `--debug` | Zapne podrobný výstup včetně warningu pro chybějící soubor |

**Příklady:**

```bash
oci-patch cat \
  --src-insecure \
  docker://harbor.cetin/tsm/stage/tsm-gateway:2.2-test-latest \
  /app/resources/git.properties

# Chybějící soubory jsou tiché, pokud nepoužiješ --debug
oci-patch cat --debug docker://registry.example.com/app:latest /missing/file
```

### ls

```
oci-patch ls [flagy] IMAGE CESTA
```

Vypíše položky pod zadanou cestou v image. Chybějící cesta je defaultně tichá a warning se zobrazí jen s `--debug`.

**Flagy:**

| Flag | Popis |
|------|-------|
| `--creds user:pass` | Credentials pro registry |
| `--override-os linux` | Výběr OS z multi-platform indexu |
| `--override-arch arm64` | Výběr architektury |
| `--override-variant v8` | Výběr varianty |
| `--insecure` / `--src-insecure` | Přeskočí TLS ověření certifikátu zdrojového registry |
| `--debug` | Zapne podrobný výstup včetně warningu pro chybějící cestu |

**Příklad:**

```bash
oci-patch ls --src-insecure docker://registry.example.com/app:latest /app/resources
```

### tree

```
oci-patch tree [flagy] IMAGE CESTA
```

Vypíše strom pod zadanou cestou. Chybějící cesta je defaultně tichá a warning se zobrazí jen s `--debug`.

**Flagy:**

| Flag | Popis |
|------|-------|
| `--creds user:pass` | Credentials pro registry |
| `--override-os linux` | Výběr OS z multi-platform indexu |
| `--override-arch arm64` | Výběr architektury |
| `--override-variant v8` | Výběr varianty |
| `--insecure` / `--src-insecure` | Přeskočí TLS ověření certifikátu zdrojového registry |
| `--debug` | Zapne podrobný výstup včetně warningu pro chybějící cestu |

**Příklad:**

```bash
oci-patch tree --src-insecure docker://registry.example.com/app:latest /app
```

---

## Credentials

Credentials se načítají v tomto pořadí priority:

1. CLI flagy `--src-creds` / `--dest-creds` / `--creds` (`uživatel:heslo`)
2. `~/.docker/config.json` (automaticky, včetně credential helpers jako `docker-credential-ecr-login`)
3. Anonymní přístup (veřejné registry)

Credentials pro pomocné zdrojové image lze deklarovat i přímo v recipe souboru (viz [Multi-source recipes](#multi-source-recipes)).

---

## Recipe

Recipe je YAML soubor, který deklaruje posloupnost kroků aplikovaných na image při kopii. Jeden `copy` příkaz může přijmout více recipe — aplikují se v pořadí.

### Struktura recipe

```yaml
name: "my-recipe"
description: "Lidsky čitelný popis"

# Volitelné: auth pro registry používané jako zdroje uvnitř recipe
registries:
  - url: registry.example.com
    username: user
    password: pass
  - url: docker.io
    # anonymní přístup

# Volitelné: pojmenované zdrojové image (pro copy-file-from, extract-file, use-base, shadow)
sources:
  - id: original
    ref: "{{.SourceImage}}"   # image předaná přes --from na CLI
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21

steps:
  - action: add-file
    src: ./utils/mytool
    dst: /usr/local/bin/mytool
    mode: "0755"
```

### Přehled akcí

#### Operace se soubory

**`add-file`** — Přidá lokální soubor do image jako novou vrstvu.

```yaml
- action: add-file
  src: ./utils/encjson          # lokální cesta
  dst: /usr/local/bin/encjson   # cesta uvnitř image
  mode: "0755"                  # volitelné, oktalový string, výchozí 0644
  owner: "1001:1001"            # volitelné uid:gid, výchozí 0:0
  condition:
    if-not-exists: /usr/local/bin/encjson   # volitelné, viz Podmínky
```

`src` může obsahovat glob pattern jako `./target/uberjar/*SNAPSHOT.jar`, ale musí odpovídat právě jednomu souboru. Když `dst` končí lomítkem, bere se jako adresář a cílový název souboru se vezme z nalezeného `src`.

**`add-dir`** — Přidá všechny soubory z lokálního adresáře jako novou vrstvu.

```yaml
- action: add-dir
  src: ./configs/
  dst: /etc/myapp/
  mode: "0644"
  owner: "1001:1001"
```

**`remove-file`** — Odstraní soubor z image (přidá whiteout vrstvu).

```yaml
- action: remove-file
  dst: /etc/default/passwords
```

**`remove-dir`** — Odstraní podstrom adresáře z image (přidá whiteout vrstvu na kořen adresáře).

```yaml
- action: remove-dir
  dst: /usr/share/java/maven-3
```

**`replace-file`** — Odstraní každý výskyt `find-all` (podle basename) z image, pak přidá `src` na `dst`. Pokud nic nenajde, soubor pouze přidá. Viz [tabulka strategií](#podmínky-a-strategie-přepisování) pro rozhodnutí kdy použít tuto akci vs `add-file`.

```yaml
- action: replace-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  find-all: encjson          # basename k nalezení a whiteoutování (volitelné)
  owner: "0:0"
```

**`copy-file-from`** — Zkopíruje soubor z jiného pojmenovaného zdrojového image do aktuálního image.

```yaml
- action: copy-file-from
  from: original          # id zdroje definované v sources:
  src: /app/dist/app.jar
  dst: /app/dist/app.jar
  mode: "0644"
  owner: "1001:1001"
```

**`extract-file`** — Extrahuje soubor z pojmenovaného zdrojového image na lokální disk.

```yaml
- action: extract-file
  from: original
  src: /app/dist/app.jar
  dst-local: /tmp/extracted/app.jar
```

**`unzip-to`** — Rozbalí zip/jar soubor, který se nachází uvnitř image, do adresáře uvnitř image.

```yaml
- action: unzip-to
  src: /app/dist/app.jar      # cesta zip souboru uvnitř image
  dst: /app/dist/extracted/   # cílový adresář uvnitř image
  owner: "1001:1001"
```

#### Base image

**`use-base`** — Nahradí aktuální pracovní image jiným base image. Následující kroky staví nad ním.

```yaml
- action: use-base
  from: alpine-base     # id zdroje definované v sources:
```

`use-base` je deklarativní obdoba `FROM alpine-base` uprostřed multi-stage Dockerfile. Do té chvíle recipe pracuje s image předanou na CLI. Po `use-base` se aktuální pracovní image přepne na zvolený zdroj a další kroky zapisují už do něj. Když potřebuješ po přepnutí vzít soubor z původní source image, dej původní image do `sources` jako `ref: "{{.SourceImage}}"` a použij `from: original` u kroků jako `copy-file-from` nebo `shadow`.

**`shadow`** — Zabalí strom souborů do šifrované self-extracting binárky a původní cestu v image nahradí touto bootstrap binárkou. Bez `from:` čte z aktuální pracovní image, s `from:` může číst z jiné pojmenované source image a zapisovat do aktuální image.

```yaml
- action: shadow
  path: /app
  extract-to: /app
  output: /app/app.self
```

```yaml
- action: shadow
  from: original
  path: /app
  extract-to: /app
  output: /app/app.self
```

#### Metadata

**`set-env`** — Nastaví nebo přepíše env proměnnou.

```yaml
- action: set-env
  key: MY_VAR
  value: "hello"
```

**`set-label`** — Nastaví OCI label.

```yaml
- action: set-label
  key: org.opencontainers.image.version
  value: "2.3.1"
```

**`set-entrypoint`** — Nastaví ENTRYPOINT image.

```yaml
- action: set-entrypoint
  value_list: ["/usr/local/bin/myapp", "--config", "/etc/myapp.conf"]
```

**`set-cmd`** — Nastaví CMD image.

```yaml
- action: set-cmd
  value_list: ["--port", "8080"]
```

**`set-user`** — Nastaví USER image (pouze metadata — uživatele nevytváří).

```yaml
- action: set-user
  value: "1001:1001"
```

**`set-workdir`** — Nastaví WORKDIR image.

```yaml
- action: set-workdir
  value: /app
```

**`fix-permissions`** — Znovu sestaví zadanou cestu s novým ownerem a/nebo módovými bity. Hodí se hlavně pro OpenShift-safe zapisovatelné adresáře jako `/app`.

```yaml
- action: fix-permissions
  path: /app
  owner: "1001:0"
  mode: g=u
  setgid-dirs: true
```

`mode` může být oktalový string jako `"0775"` nebo speciální hodnota `g=u`, která zkopíruje user permission bity na group bity. Když nastavíš `setgid-dirs: true`, adresáře v přestavěném stromu dostanou setgid bit.

### Podmínky a strategie přepisování

Tabulka níže shrnuje všechny dostupné strategie pro řízení toho, zda a jak soubor do image skončí.

| Záměr | Jak zapsat | Co se stane |
|-------|-----------|-------------|
| Vždy přepsat na `dst` | `add-file` bez `condition` | Přidá se nová vrstva — starý soubor na `dst` je zastiňen. Soubory na jiných cestách zůstanou. |
| Přidat jen pokud přesná cesta neexistuje | `condition: if-not-exists: /usr/bin/tool` | Zkontroluje přesnou cestu napříč všemi vrstvami. Pokud nalezena, přeskočí. |
| Přidat jen pokud basename neexistuje v adresářích `$PATH` | `condition: if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]` | Prohledá zadané adresáře podle basename. Přeskočí pokud nalezeno v **libovolném** z nich. Použij tehdy, kdy nevíš přesnou cestu. |
| Odstraň všechny výskyty daného názvu a umísti na `dst` | `replace-file` + `find-all: nazevnastroje` | Projde všechny vrstvy, whiteoutuje každou cestu kde basename odpovídá, pak přidá soubor na `dst`. Pokud nic nenajde, jen přidá. Výsledek: přesně jedna kopie na přesně jednom místě. |

**`add-file` s `if-not-exists`** (přesná cesta):

```yaml
- action: add-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  condition:
    if-not-exists: /usr/local/bin/encjson
```

**`add-file` s `if-not-exists-in`** (hledání basename v adresářích):

```yaml
- action: add-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  condition:
    if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]
```

**`replace-file`** — odstraní všechny výskyty, přidá na `dst`:

```yaml
- action: replace-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  find-all: encjson        # basename k nalezení a whiteoutování v celém image
```

`replace-file` bez `find-all` se chová stejně jako `add-file` bez podmínky (prostý přepis na `dst`).

### Multi-source recipes

Recipe může deklarovat více pojmenovaných zdrojových image. Speciální reference `{{.SourceImage}}` odkazuje na image předanou přes `--from` na CLI.

```yaml
sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21
  - id: tools
    ref: docker://registry.example.com/internal/tools:latest
```

Kroky pak mohou kterýkoli z těchto zdrojů používat přes `from: <id>`. Je to ekvivalent Docker multi-stage buildů — ale deklarativní, bez daemona a s kombinováním zdrojů z různých registrií.

To samé platí i pro `shadow`: můžeš vzít `/app` z původní source image, přepnout se přes `use-base` na jinou base image a do nové image vložit už jen `/app/app.self`.

### `--reduce` a `--reduce-changes`

`--reduce` nedělá jen klasický squash vrstev. Materializuje efektivní finální filesystem po všech recipe krocích do jediné vrstvy, takže cesty odstraněné nebo nahrazené přes whiteout se už do výstupu nepřenášejí.

To je užitečné hlavně se `shadow`, kde finální image může obsahovat jen:

- obsah nové base image
- `/app/app.self`
- minimální bootstrap metadata adresáře `/app`

Výsledná velikost tak bývá blízko původní image plus overhead `app.self`, ne původní `/app` plus `app.self`.

`--reduce-changes` řeší jiný případ: chceš zachovat původní base image vrstvy kvůli cache/reuse, ale nechceš mít mnoho malých vrstev z recipe. Výstup pak obsahuje:

- všechny vrstvy base image
- jednu novou delta vrstvu s efektivními změnami z recipe

Když recipe nepoužije `use-base`, jako base se bere source image z CLI. Když recipe použije `use-base`, jako base se bere image z posledního `use-base` kroku a delta se počítá vůči ní.

Prakticky:

```bash
# Jedna výsledná vrstva s celým finálním filesystemem.
oci-patch copy --recipe app.yml --reduce SOURCE DEST

# Původní/base vrstvy zůstanou, změny z recipe budou v jedné vrstvě navíc.
oci-patch copy --recipe app.yml --reduce-changes SOURCE DEST
```

---

## Příklady

### Injekce binárky do existujícího image

```yaml
# inject-encjson.yml
name: "inject-encjson"
description: "Injektuje utilitu encjson — jen pokud ještě není přítomna"

steps:
  - action: add-file
    src: ./utils/encjson
    dst: /usr/local/bin/encjson
    mode: "0755"
    condition:
      if-not-exists: /usr/local/bin/encjson

  - action: set-label
    key: "org.datalite.patched-by"
    value: "oci-patch"
```

```bash
oci-patch copy \
  --src-creds user:pass --dest-creds user:pass \
  --recipe inject-encjson.yml \
  docker://registry.datalite.cz/tsm/tsm-gateway:2.3-latest \
  docker://registry.datalite.cz/tsm/tsm-gateway:2026-04-17.01
```

### Rebase Java aplikace na čistý Alpine image

```yaml
# rebase-app.yml
name: "rebase-app-on-alpine"
description: "Extrahuje app.jar ze zdrojového image, přestaví na čistém Alpine"

sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21

steps:
  - action: use-base
    from: alpine-base

  - action: copy-file-from
    from: original
    src: /app/dist/app.jar
    dst: /app/dist/app.jar
    mode: "0644"
    owner: "1001:1001"

  - action: unzip-to
    src: /app/dist/app.jar
    dst: /app/dist/extracted/
    owner: "1001:1001"

  - action: set-user
    value: "1001:1001"

  - action: set-workdir
    value: /app

  - action: set-entrypoint
    value_list: ["java", "-jar", "/app/dist/app.jar"]

  - action: set-label
    key: "org.datalite.base"
    value: "alpine:3.21"
```

```bash
oci-patch copy \
  --src-creds user:pass --dest-creds user:pass \
  --recipe rebase-app.yml \
  docker://registry.example.com/myapp:2.3-latest \
  docker://registry.example.com/myapp:2026-04-17.alpine
```

### Shadow `/app` přímo v image

```yaml
# shadow.yml
name: "shadow /app folder"
description: "Replace /app with encrypted self-extracting bootstrap and make it OpenShift-safe"

steps:
  - action: shadow
    path: /app
    extract-to: /app
    output: /app/app.self

  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
```

```bash
oci-patch copy \
  --recipe shadow.yml \
  --reduce \
  docker://registry.example.com/myapp:latest \
  docker://registry.example.com/myapp:shadowed
```

### Shadow `/app` na nové base image

```yaml
# shadow-from-base.yml
name: "shadow /app onto a new base"

sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: runtime-base
    ref: docker://registry.example.com/runtime-base:latest

steps:
  - action: use-base
    from: runtime-base

  - action: shadow
    from: original
    path: /app
    extract-to: /app
    output: /app/app.self

  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
```

Tenhle pattern se hodí, když chceš zachovat aplikační payload ze source image, ale runtime image postavit na menší nebo čistší base image.

### Poznámka k OpenShiftu

Pro OpenShift se nespoléhej jen na fixní runtime UID. Bezpečnější pattern je udělat `/app` zapisovatelný přes group `0` a zkopírovat user bity na group bity:

- `owner: "1001:0"`
- `mode: g=u`
- `setgid-dirs: true`

Tím zůstane bootstrap adresář zapisovatelný i pro libovolné non-root UID běžící s group `0`, což je častý deployment model v OpenShiftu.

### Náhrada skopeo v existujícím projektu

Pokud dnes používáte `skopeo` pro `inspect` a `copy`, stačí změnit cestu k binárce:

```rust
// před
let service = SkopeoService::new("skopeo".to_string());

// po
let service = SkopeoService::new("/usr/local/bin/oci-patch".to_string());
```

Všechny flagy (`--src-creds`, `--dest-creds`, `--debug`, `--dest-no-reuse`, `--creds`, `--override-arch`, `--override-os`) jsou identické.

---

## Spuštění testů

```bash
go test ./...
```

Testy jsou plně offline — nevyžadují přístup k žádnému registry. Používají in-memory OCI image sestavené z tar vrstev.

```
ok  github.com/datalite/oci-patch/internal/image    (17 testů)
ok  github.com/datalite/oci-patch/internal/recipe   (11 testů)
```
