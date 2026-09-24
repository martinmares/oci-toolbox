# oci-toolbox

**Nástroj pro přenos a úpravu OCI image — bez Docker daemona.**

`oci-toolbox` kopíruje container image mezi registry (podobně jako `skopeo`) a navíc při přenosu aplikuje deklarativní *recipe*, které image upravují — přidávají soubory, přepisují metadata, mění base image a podobně. Vše běží jako jediná statická binárka bez jakýchkoli runtime závislostí.

---

## Obsah

- [Proč oci-toolbox](#proč-oci-toolbox)
- [Srovnání se skopeo](#srovnání-se-skopeo)
- [Instalace](#instalace)
- [Příkazy](#příkazy)
  - [copy](#copy)
  - [inspect](#inspect)
  - [cat](#cat)
  - [ls](#ls)
  - [tree](#tree)
  - [Příkaz examples](#příkaz-examples)
  - [bundle](#bundle)
  - [release to-bundle](#release-to-bundle)
  - [release reconstruct](#release-reconstruct)
  - [release selectors a compare](#release-selectors-a-compare)
  - [release verify](#release-verify)
  - [Údržba registry](#údržba-registry)
  - [Snapshot Kubernetes images](#snapshot-kubernetes-images)
- [Git repository pro releases](#git-repository-pro-releases)
- [Credentials](#credentials)
- [Recipe](#recipe)
  - [Struktura recipe](#struktura-recipe)
  - [Přehled akcí](#přehled-akcí)
  - [Podmínky (conditions)](#podmínky-conditions)
  - [Multi-source recipes](#multi-source-recipes)
- [Příklady](#příklady)
- [Spuštění testů](#spuštění-testů)

---

## Proč oci-toolbox

Typický postup pro úpravu container image dnes vypadá takto:

```
docker pull image
docker run --entrypoint ... image   # vykuchání souboru
docker build -f Dockerfile.patch .  # sestavení upraveného image
docker push patched-image
```

To vyžaduje běžící Docker daemon, Dockerfile a mezikontejnery. `oci-toolbox` celý tento postup nahradí jediným příkazem s YAML recipe — žádný daemon, žádný Dockerfile, žádné mezikontejnery, žádná binárka `docker`.

---

## Srovnání se skopeo

`oci-toolbox` je **záměrně CLI-kompatibilní** se `skopeo` pro příkazy, které implementuje (`copy`, `inspect`, `--version`). Stačí nahradit binárku `skopeo` za `oci-toolbox` — stávající skripty fungují beze změny argumentů.

| Funkce | skopeo | oci-toolbox |
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

`skopeo copy` je čistě přenosový nástroj — přesune data z A do B. `oci-toolbox copy` image při průchodu modifikuje: stáhne vrstvy, aplikuje kroky z recipe (každý krok přidá novou OCI vrstvu nebo přepíše config image), výsledek pushne. Původní image zůstane nedotčen.

**2. Žádný `RUN` — záměrně**

Spouštění příkazů uvnitř kontejneru při buildu (jako `apk upgrade`, `adduser`) vyžaduje container runtime (runc, crun nebo buildkit). `oci-toolbox` tuto funkcionalitu záměrně vynechává, aby zůstal statickou binárkou bez závislostí. Vše co dělá je čistá manipulace s vrstvami a configem image v paměti.

**3. Multi-source kompozice**

Recipe může deklarovat více pojmenovaných zdrojových image. Kroky mohou ze kteréhokoli z nich extrahovat soubory a skládat je do nového image — podobně jako Docker multi-stage builds, ale deklarativně a bez daemona.

**4. Podmíněné kroky**

Krok může být podmíněn pomocí `condition: if-not-exists: /cesta` — pokud soubor v aktuálním stavu image již existuje, krok se přeskočí. To umožňuje idempotentní patchování: nástroj se injektuje jen tehdy, pokud ještě není přítomen.

**5. CLI kompatibilita se skopeo**

Všechny flagy používané v nejběžnějších skopeo workflow jsou podporovány se stejnými názvy:

```bash
# skopeo příkazy fungují identicky jako oci-toolbox
skopeo inspect --creds user:pass docker://registry.example.com/app:latest
skopeo copy --src-creds u:p --dest-creds u:p docker://src/img:tag docker://dst/img:tag
skopeo copy --debug --dest-no-reuse docker://src docker://dst
skopeo --version
```

---

## Instalace

```bash
git clone https://github.com/martinmares/oci-toolbox
cd oci-toolbox
go build -o oci-toolbox .
```

Cross-kompilace pro Linux amd64:

```bash
GOOS=linux GOARCH=amd64 go build -o oci-toolbox-linux-amd64 .
```

S nástrojem [just](https://just.systems/) se lokální binárky ve stejném stylu jako release zapisují do `dist/`:

```bash
just build-linux
just build-macos
just build-windows
```

---

## Příkazy

### copy

```
oci-toolbox copy [flagy] ZDROJ CÍL
```

Zkopíruje image ze ZDROJE do CÍLE a v pořadí aplikuje všechny zadané recipe.

U čisté kopie registry→registry bez recipe, redukce vrstev, entrypoint wrappingu a `--dest-no-reuse` se před stahováním porovnají manifest digesty. Pokud cílový tag už ukazuje na stejný digest, nepřenášejí se žádné vrstvy. Pokud cílový tag chybí nebo ukazuje jinam, ale stejný digest už existuje v cílovém repository pod jiným tagem, vytvoří se pouze požadovaný tag přes registry API. Pull/push vrstev začne až tehdy, když cílové repository zdrojový digest skutečně neobsahuje.

**Flagy:**

| Flag | Popis |
|------|-------|
| `--src-creds user:pass` | Credentials pro zdrojový registry |
| `--dest-creds user:pass` | Credentials pro cílový registry |
| `--src-proxy URL\|direct` | Proxy použitá pouze pro operace se zdrojovým registry |
| `--dest-proxy URL\|direct` | Proxy použitá pouze pro operace s cílovým registry |
| `--recipe cesta/k/recipe.yml` | Recipe soubor (lze opakovat) |
| `--debug` | Zapne podrobný výstup |
| `--batch-file cesta/k/images.yml` | Spustí více copy jobů ze YAML souboru |
| `--release-manifest cesta/k/release.yml` | Zkopíruje všechny image uzamčené digestem z release manifestu |
| `--src-base registry/cesta` | Zdrojový registry/repository základ nahrazený v release-manifest režimu; výchozí je `registry_base` |
| `--dest-base registry/cesta` | Povinný cílový registry/repository základ v release-manifest režimu |
| `--output cesta/k/release.yml` | Zapíše release manifest pro zkopírované cílové image |
| `--release-id ID` | Release ID pro výstup z `--batch-file`, nebo přepsání cílových tagů a výstupu v režimu `--release-manifest` |
| `--retries počet` | Dodatečné pokusy při přechodných síťových chybách; výchozí `4`, hodnota `0` retry vypne |
| `--retry-initial-delay doba` | Prodleva před prvním retry; výchozí `2s` |
| `--retry-max-delay doba` | Maximální exponenciální prodleva; výchozí `30s` |
| `--continue-on-error` | V batch režimu pokračuje další položkou po selhání |
| `--wrap-entrypoint` | Pro všechny kroky `shadow` obalí ENTRYPOINT a CMD z CLI image `SOURCE`; vhodné s `use-base` |
| `--no-wrap-entrypoint` | Vypne entrypoint wrapper pro všechny kroky `shadow` v načtených recipe |
| `--dest-tag tag` | Přidá další cílový tag ve stejném repository bez opakovaného kopírování vrstev (lze opakovat) |
| `--dest-no-reuse` | Zakáže cross-repository blob mount/reuse v cíli (fallback při problémech) |
| `--reduce` | Materializuje finální filesystem do jediné minimální vrstvy |
| `--reduce-changes` | Zachová base vrstvy a změny z recipe sloučí do jedné delta vrstvy |
| `--progress` | Vykreslí terminálové progress bary |
| `--progress-json` | Emituje machine-readable progress eventy jako JSONL |

Směrování přes proxy je oddělené pro zdroj a cíl. Externí image lze například stáhnout přes firemní Squid proxy a do interní registry ji odeslat přímo:

```bash
oci-toolbox copy \
  docker://registry.external.example/team/app:1.2 \
  docker://registry.internal.example/team/app:1.2 \
  --src-proxy http://proxy.example.com:8080 \
  --dest-proxy direct
```

Pokud proxy flag není uvedený, zůstává zachované standardní chování proměnných `HTTP_PROXY`, `HTTPS_PROXY` a `NO_PROXY`. Speciální hodnota `direct` proxy z prostředí pro daný směr vypne. Proxy se používá pro kontroly manifestů a digestů, pull, push, reuse blobů i vzdálené tagování. HTTP proxy používá pro HTTPS registry metodu CONNECT, takže registry credentials a obsah image zůstávají uvnitř TLS tunelu, pokud proxy neprovádí TLS interception. Credentials vložené do URL obyčejné HTTP proxy jsou odmítnuty; autentizovaná proxy URL musí používat HTTPS.

**Příklady:**

```bash
# Jednoduchá kopie bez recipe
oci-toolbox copy docker://registry.example.com/app:1.0 docker://registry.example.com/app:1.0-copy

# Kopie s credentials
oci-toolbox copy \
  --src-creds srcuser:srcpass \
  --dest-creds dstuser:dstpass \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Jedna kopie a další cílové tagy pro stejný manifest
oci-toolbox copy \
  --dest-creds dstuser:dstpass \
  --dest-tag latest \
  --dest-tag stable \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Kopie s recipe (injekce nástroje)
oci-toolbox copy \
  --src-creds user:pass \
  --dest-creds user:pass \
  --recipe inject-tool.yml \
  docker://registry.example.com/app:latest \
  docker://registry.example.com/app:patched

# Více recipe aplikovaných v pořadí
oci-toolbox copy \
  --recipe recipe-1.yml \
  --recipe recipe-2.yml \
  docker://src/app:tag \
  docker://dst/app:tag

# Zdroj podle digestu (SHA256)
oci-toolbox copy \
  --src-creds user:pass \
  docker://registry.example.com/app@sha256:abc123... \
  docker://registry.example.com/app:stable

# Fallback s --dest-no-reuse při chybě blob reuse
oci-toolbox copy --debug --dest-no-reuse \
  --src-creds user:pass --dest-creds user:pass \
  docker://src/app:tag docker://dst/app:tag

# Machine-readable progress pro integrace
oci-toolbox copy --progress-json \
  docker://src/app:tag docker://dst/app:tag
```

### Batch copy

Pro opakované kopírování více image můžeš použít `--batch-file`. Stejné CLI flagy se aplikují na všechny položky v souboru.

```yaml
# images.yml
copy:
  - src: registry.example.com/team/config:release-1.0
    dest: registry.example.com/team/config:test-latest
    id: config
    app_name: config-service
    container_name: application
    dest_tags:
      - latest
      - stable
  - src: registry.example.com/team/ticket:release-1.0
    dest: registry.example.com/team/ticket:test-latest
```

```bash
oci-toolbox copy \
  --batch-file images.yml \
  --release-id RE_2026.08.10.01 \
  --output releases/RE_2026.08.10.01-dev.yml \
  --src-insecure \
  --dest-insecure \
  --retries 5 \
  --retry-initial-delay 2s \
  --retry-max-delay 30s \
  --progress
```

Batch běží sériově. Bez `--continue-on-error` se zastaví na první chybě. S `--continue-on-error` pokračuje dalšími položkami, ale pokud aspoň jedna položka selže, příkaz na konci vrátí nenulový exit code a vypíše souhrn selhání.

Primární klíče v batch souboru jsou `src` a `dest`, stejně jako CLI flagy `--src-*` a `--dest-*`. Jako aliasy jsou podporované i `source` a `destination`. `dest_tags` přidá další tagy ve stejném cílovém repository po jednom zkopírování image, stejně jako opakované `--dest-tag` u jednoho CLI příkazu. Volitelné hodnoty `id`, `app_name` a `container_name` určují deployment identitu ve výstupním release manifestu; pokud chybějí, odvodí se z názvu cílového repository. Duplicitní cílové image, ID a selectory jsou odmítnuté při validaci souboru.

Kombinace `--batch-file` a `--output` po úspěšném dokončení všech kopií znovu načte cílové manifesty z registry a zapíše jejich skutečné digesty, tagy a platformy. Zdrojová reference a její digest se zaznamenají do `source`. `--release-id` je volitelný pouze tehdy, když všechny cíle používají stejný tag; v takovém případě se tento tag použije jako release ID. Výstup se zapisuje atomicky a existující soubor se nepřepíše.

### Kopírování release manifestu

Pomocí `--release-manifest` lze zkopírovat neměnný release do jiné registry cesty bez předchozího generování batch souboru:

```bash
oci-toolbox copy \
  --release-manifest releases/2026.06.25.01.yml \
  --src-base quay.example.com/nac-test \
  --dest-base artifactory.example.com/nac-test \
  --release-id RE_2026.09.10.02 \
  --output releases/RE_2026.09.10.02-replica.yml \
  --src-insecure \
  --dest-insecure \
  --retries 5 \
  --retry-initial-delay 2s \
  --retry-max-delay 30s \
  --progress
```

Každý zdroj používá `image@digest` z manifestu. Zdrojový základ se nahradí hodnotou `--dest-base` a zbývající repository cesta se zachová. Ve výchozím stavu dostane každý cíl jako tag `release_id` ze vstupního manifestu; `--release-id` tento cílový tag přepíše, aniž by změnil kopírovaný source digest. Pokud není zadán `--src-base`, použije se `registry_base` z manifestu; pokud není dostupné ani jedno, příkaz skončí před kopírováním. Image mimo zdrojový základ a duplicitní cíle jsou odmítnuty. Hodnoty `tag` a `extra_tags` z manifestu se záměrně nekopírují. Původní flagy `--src-prefix` a `--dest-prefix` byly přejmenovány; jejich použití skončí chybou, která odkáže na `--src-base`, respektive `--dest-base`.

`--release-manifest`, `--batch-file` a poziční `SOURCE DESTINATION` jsou samostatné vstupní režimy. Credentials, TLS nastavení, progress, debug výstup a `--continue-on-error` se aplikují na každý vygenerovaný copy job.

Každý zkopírovaný cílový digest se ověří proti release manifestu. Multi-platform image indexy se mezi registry kopírují bez výběru jedné platformy. Recipe, redukce vrstev, entrypoint wrapping, `--dest-no-reuse` a `--dest-tag` nejsou v release-manifest režimu podporovány, protože by mohly uzamčený obsah změnit nebo materializovat jen částečně.

Při použití `--output` se soubor zapíše atomicky až po úspěšném dokončení všech kopií a ověření digestů. Nový manifest zachová původní deployment selektory, platformy a bundle metadata; při použití `--release-id` použije nové release ID, `registry_base` a jednotlivé `image` přesměruje na cíl a předchozí release image zaznamená do `source` každé položky. Existující výstupní soubor se nikdy nepřepíše. Bez `--output` se `--release-id` stále použije pro cílové tagy, pouze se nezapíše manifest.

Přechodné síťové chyby jako timeout, reset spojení, broken pipe, dočasná chyba registry nebo unexpected EOF zopakují aktuální image s exponenciálním backoffem. Výchozí čtyři retry čekají `2s`, `4s`, `8s` a `16s`. Dokončené batch položky se neopakují a autentizační, validační, digestové ani jiné trvalé chyby se neopakují vůbec. `--continue-on-error` se uplatní až poté, co položka vyčerpá všechny retry pokusy.

### inspect

```
oci-toolbox inspect [flagy] IMAGE
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
oci-toolbox inspect docker://docker.io/library/alpine:3.21

# S credentials
oci-toolbox inspect --creds user:pass docker://registry.example.com/app:latest

# Registry s nestandardním/self-signed certifikátem
oci-toolbox inspect --src-insecure docker://registry.example.com/app:latest

# Konkrétní platforma z multi-arch indexu
oci-toolbox inspect \
  --override-os linux \
  --override-arch arm64 \
  docker://docker.io/library/alpine:3.21

# Rychlý inspect bez načítání tagů
oci-toolbox inspect --no-tags docker://registry.example.com/app:latest
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
oci-toolbox cat [flagy] IMAGE CESTA
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
oci-toolbox cat \
  --src-insecure \
  docker://registry.example.com/team/gateway:2.2-test-latest \
  /app/resources/git.properties

# Chybějící soubory jsou tiché, pokud nepoužiješ --debug
oci-toolbox cat --debug docker://registry.example.com/app:latest /missing/file
```

### ls

```
oci-toolbox ls [flagy] IMAGE CESTA
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
oci-toolbox ls --src-insecure docker://registry.example.com/app:latest /app/resources
```

### tree

```
oci-toolbox tree [flagy] IMAGE CESTA
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
oci-toolbox tree --src-insecure docker://registry.example.com/app:latest /app
```

### spring-config

```
oci-toolbox spring-config discover [flagy] IMAGE
```

Analyzuje Spring Boot image vytvořený pomocí JIB bez jeho spuštění. Příkaz přečte JIB classpath a main-class soubory, zachová pořadí classpath, projde classpath adresáře i JAR/ZIP archivy a exportuje kandidáty Spring konfigurace spolu s deterministickým `manifest.yml`.

Ve výchozím stavu extrahuje:

- `application.yml`, `application.yaml`, `application.properties` a profilově pojmenované varianty z libovolného místa classpath;
- `bootstrap.yml`, `bootstrap.yaml`, `bootstrap.properties` a profilově pojmenované varianty;
- kořenové `.yml`, `.yaml` a `.properties` soubory z každého classpath adresáře nebo archivu, včetně vlastních defaultů jako `database-copy-defaults.yml`;
- další cesty vybrané opakovaným globem `--include`.

```bash
oci-toolbox spring-config discover \
  docker://registry.example.com/team/application:2.4 \
  --output ./spring-config-analysis \
  --include 'config/*.yml' \
  --progress \
  --src-insecure
```

**Flagy:**

| Flag | Popis |
|------|-------|
| `--output`, `-o` | Povinný nový výstupní adresář; existující adresář se nikdy nepřepíše |
| `--classpath-file /app/jib-classpath-file` | JIB classpath soubor uvnitř image |
| `--main-class-file /app/jib-main-class-file` | JIB main-class soubor uvnitř image |
| `--include glob` | Další glob pro cestu v archivu nebo basename; lze opakovat |
| `--progress` | Průběžný výpis při čtení vrstev image a classpath archivů |
| `--creds user:pass` | Credentials pro registry |
| `--override-os linux` | Výběr OS z multi-platform indexu |
| `--override-arch amd64` | Výběr architektury z multi-platform indexu |
| `--override-variant v8` | Výběr varianty platformy |
| `--insecure` / `--src-insecure` | Přeskočení TLS ověření zdrojového registry |
| `--debug` | Zapnutí podrobného výstupu |

Výstup seskupuje soubory podle indexu classpath, takže se stejné názvy z různých JARů nepřepíšou:

```text
spring-config-analysis/
├── manifest.yml
├── manifest.svg
├── classpath.txt
└── sources/
    ├── 0000-resources/application.yml
    └── 0047-application-commons.jar/application-commons.yml
```

Manifest obsahuje digest a platformu image, main class, seřazenou classpath, původní image nebo `jar!/entry` zdroj, výstupní cestu, SHA-256 a velikost. Název jako `application-commons.yml` dostane `profile_hint: commons`, ale `activation` zůstane `unknown`: soubor může být načten profilem, přes `spring.config.import`, anotaci `@PropertySource` nebo vlastním kódem.

`manifest.svg` je prohledávatelná vizuální mapa od image přes položky classpath ke konfiguračním ID, zdrojům a výstupním souborům. Zobrazuje pouze položky classpath s nalezenými konfiguracemi a uvádí počet vynechaných prázdných položek. Zdroj z JARu je označen `JAR`, v poli `source` obsahuje `!/` a v `manifest.yml` má `archive_entry`; přímé soubory image jsou seskupené pod classpath `directory`, například `/app/resources`.

Příkaz provádí pouze statickou inventuru. Soubory neslučuje a nerekonstruuje výsledný Spring Environment. Runtime analýza stále potřebuje environment variables a JVM argumenty deploymentu, ConfigMaps a Secrets, Spring Cloud Config zdroje, aktivní profily a ideálně snapshoty Actuator `/actuator/env` a `/actuator/configprops`.

Výstup může obsahovat credentials. Výstupní adresář používá práva `0700` a extrahované soubory `0600`.

### Příkaz examples

```
oci-toolbox examples
```

Vypíše přímo v terminálu strukturovanou kuchařku. Každý příkaz má výraznou hlavičku a popis, pod kterými jsou pojmenované jednotlivé varianty. Shell příkazy a potřebné externí soubory, například bundle, batch, registry konfigurace a recipe YAML, jsou zobrazené jako samostatné bloky.

```bash
oci-toolbox examples

# Ekvivalentní forma přes Cobra help:
oci-toolbox help examples
```

Pokud výstup míří do terminálu, hlavičky příkazů, názvy variant, shell bloky a bloky externích souborů používají odlišné barvy. Při pipe nebo přesměrování se stejná hierarchie zachová v čistém textu, takže funguje i `oci-toolbox examples | less` a `oci-toolbox examples > examples.txt`.

### bundle

Bundle je měnitelná deklarace spravovaná v Gitu. Určuje, které tagy nebo digesty image se mají publikovat jako jeden release. Neobsahuje credentials a používá striktní YAML: neznámé položky, duplicitní ID, duplicitní deployment selektory a duplicitní cílové repository jsou odmítnuty.

```yaml
name: stable
description: Stable application bundle

registries:
  source:
    url: registry-source.example.com
  target:
    url: registry-release.example.com
    project: release

extra_tags:
  - stable

images:
  - id: config
    source:
      repository: team/config
      tag: 2.3-latest
    target:
      repository: config
    deployment:
      app_name: config
      container_name: config
```

Každý `source` musí obsahovat právě jednu položku `tag` nebo `digest`. Registry URL a project cesty lze při spuštění nahradit pomocí `--src-registry`, `--dest-registry`, `--src-project` a `--dest-project`.

```bash
# Lokální validace bez přístupu do registry
oci-toolbox bundle validate bundle.yml

# Výpis výsledných zdrojových a cílových referencí
oci-toolbox bundle plan bundle.yml \
  --release-id RE_2026.07.28.01

# Kopírování image a vytvoření neměnného release záznamu s digesty
oci-toolbox bundle publish bundle.yml \
  --release-id RE_2026.07.28.01 \
  --bundle-revision "$(git rev-parse HEAD)" \
  --extra-tag candidate \
  --output releases/RE_2026.07.28.01.yml \
  --progress
```

`bundle publish` podporuje stejné nezávislé směrování `--src-proxy` a `--dest-proxy` jako `copy`. Platí pro preflight kontrolu, reuse digestů při selective release, kopírování i přetagování.

`bundle publish` před kopírováním zkontroluje všechny image. Chybějící primární release tag vytvoří, existující shodný digest považuje za idempotentní opakování a při existujícím tagu s jiným digestem skončí chybou. Extra tagy jako `stable` jsou pohyblivé aliasy. Výstupní soubor nesmí existovat. Git commit, tag ani push záměrně nejsou úlohou `oci-toolbox`; provádí je CI nebo volající skript.

#### Selektivní release z base manifestu

Pokud se mají publikovat pouze vyjmenované kontejnery aplikací a všechny ostatní digesty převzít z existujícího release, předej předchozí manifest a opakuj `--select` s deployment identitou `app_name/container_name`:

```bash
oci-toolbox bundle publish bundles/stable.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select zis-aq-bridge/application \
  --select zis-es-idx-view/application \
  --output releases/RE_2026.08.03.01.yml \
  --progress
```

Pro delší seznam použij striktní YAML soubor:

```yaml
selectors:
  - zis-aq-bridge/application
  - zis-es-idx-view/application
```

```bash
oci-toolbox bundle publish bundles/stable.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select-file selected-images.yml \
  --preserve-base-only \
  --output releases/RE_2026.08.03.01.yml
```

Selector je záměrně založený na dvojici `(app_name, container_name)`, nikoliv na měnitelném tagu nebo názvu repository. Vybrané položky použijí aktuální source z bundle. Nevybrané položky se načtou z base release podle neměnného digestu a dostanou nový tag `release_id`; jejich digest se tedy nemůže změnit. Ve výchozím režimu musí mít base release a bundle shodné deployment selektory i cílové repository. S `--preserve-base-only` se base release stane autoritativní šablonou nového release: zachová se jeho seznam i pořadí položek, vybrané bloky se nahradí daty z bundle a všechny ostatní se převezmou beze změny digestu. Nevybrané položky existující pouze v bundle se ignorují. Vybraný selector musí existovat v base release i bundle; tím se zabrání nechtěnému přidání nebo odebrání deploymentu. Chybějící, duplicitní nebo chybně zapsané selectory a změna cílové repository vybrané položky skončí chybou ještě před zápisem do registry. `--select` a `--select-file` lze kombinovat.

Pokud není nastaveno ani `--dest-registry`, ani `registries.target.url`, selective publish nejprve použije `registry_base` z base release. Když chybí, cílový prefix odvodí ze společných deployment selectorů odebráním odpovídajícího bundle `target.repository` z base image. Odvození uspěje pouze tehdy, když všechny společné položky vedou ke stejnému prefixu; explicitní destination má vždy přednost.

Pro vybrané bundle images se použijí source registry credentials. Destination registry credentials se použijí také jako source credentials při převzetí nevybraných images z předchozího release.

Pokud převzatá base image zůstává ve stejné repository, publish vytvoří nový release tag přímo přes registry API a nepřenáší žádné vrstvy. Úplný pull/push se provádí pouze pro vybrané images nebo při rozdílné source a target repository.

S `--progress` se před samotným přenosem zobrazuje samostatný `preflight` progress bar. Ukazuje počet zkontrolovaných images, právě ověřovaný selector a zda se kontroluje source digest nebo cílový tag.

Nové images vybrané z bundle zatím podporují pouze samostatné `linux/amd64` manifesty. Nevybrané a base-only images převzaté přes neměnný digest mohou být také multi-platform OCI indexy; dostanou nový tag bez výběru nebo změny konkrétní platformy.

### release to-bundle

Existující release manifest lze převést zpět na bundle, pokud se jeho zaznamenané source images mají stát vstupem dalšího publish workflow:

```bash
oci-toolbox release to-bundle releases/2026.08.10.01-dev.yml \
  --name tsm-sda-2.4 \
  --output bundles/tsm-sda-2.4.yml
```

Příkaz odvodí společné source a target registry/project prefixy a zachová source repository, target repository, ID, deployment selector a extra tagy každé image. Zachová tedy i přejmenování repository, například source `tsm-config` publikovaný jako target `tsm-config-form`. Každá release image musí obsahovat metadata `source`, všechny source images musí používat jednu registry a všechny target images také jednu registry. Nejednoznačný nebo nereprezentovatelný vstup skončí chybou, místo aby vytvořil zavádějící bundle. Název bundle nelze z každého release manifestu spolehlivě získat, proto je `--name` povinný. `--output -` (výchozí hodnota) zapisuje YAML na standardní výstup.

`--source-mode` určuje, jakou source referenci vygenerovaný bundle použije:

- `--source-mode digest` je výchozí. Zapíše source digest každé image, takže pozdější `bundle publish` použije přesně stejná data jako původní release manifest. Je to bezpečná volba pro reprodukci, rollback a audit.
- `--source-mode tag` zapíše původní source tag. Bundle se lépe používá jako měnitelný desired state pro další releases, jeho obsah se ale může při pozdějším použití změnit, pokud se tag v registry posune. Pokud některé image source tag chybí, konverze skončí chybou.

```bash
oci-toolbox release to-bundle releases/2026.08.10.01-dev.yml \
  --name tsm-sda-2.4 \
  --source-mode tag \
  --output bundles/tsm-sda-2.4-tags.yml
```

Metadata patřící pouze do release manifestu, například `release_id`, `created_at`, platforma, primární target tag a target digest, se do bundle záměrně nepřenášejí. Nový primární target tag se později předá pomocí `bundle publish --release-id`.

### release reconstruct

`release reconstruct` vytvoří stejný release manifest s digesty z image, které už v registry existují. Reference lze předat přímo:

```bash
oci-toolbox release reconstruct \
  registry.example.com/release/config:RE_2026.07.28.01 \
  registry.example.com/release/gateway:RE_2026.07.28.01 \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress
```

Každá reference musí obsahovat explicitní tag nebo digest; implicitní `latest` je odmítnut. Pokud všechny reference používají stejný tag, stane se tento tag hodnotou `release_id`. Jinak je nutné předat `--release-id`. Jako `id`, `app_name` a `container_name` se použije název repository.

Pro inicializační manifest rekonstruovaný z existujícího Kubernetes prostředí načti selektory Deploymentů přímo z API:

```bash
oci-toolbox release reconstruct \
  --namespace example \
  --image-prefix registry.example.com/team \
  --exclude-scaled-to-zero \
  --release-id RE_2026.07.28.01 \
  --kube-insecure \
  --insecure \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress
```

Tento režim zapíše do `app_name` název Deploymentu a do `container_name` název každého běžného containeru z jeho pod template. Stejnou image použitou pod různými selektory zachová vícekrát. Init containery nezahrnuje. Pomocí `--image-prefix` lze ještě před zahájením kontroly registry vyřadit platformní a operátorové images mimo vybranou registry nebo repository cestu. `--exclude-scaled-to-zero` vynechá Deploymenty s explicitním `spec.replicas: 0`; deploymenty požadující alespoň jednu repliku zůstanou zahrnuté, i když právě nemají ready pod. Výsledek popisuje aktuální topologii namespace; poziční images, `--file` a `--namespace` se vzájemně vylučují. Pokud nestačí výchozí kubeconfig, použij `--kubeconfig` a `--context`. Kubernetes TLS a registry TLS se řídí odděleně pomocí `--kube-insecure` a `--insecure`.

Pokud se deployment selektory liší od názvů repository, použij striktní YAML vstup:

```yaml
images:
  - ref: registry.example.com/release/config:RE_2026.07.28.01
    id: config
    app_name: config-service
    container_name: application
  - ref: registry.example.com/release/gateway:RE_2026.07.28.01
    app_name: gateway
    container_name: gateway
```

```bash
oci-toolbox release reconstruct \
  --file release-images.yml \
  --release-id RE_2026.07.28.01 \
  --extra-tag stable \
  --output releases/RE_2026.07.28.01.yml
```

Každá image se uloží pomocí digestu získaného z registry. Vyhledání tagů je explicitní: `--discover-tags` načte tagy repository, ověří jejich digesty a zaznamená všechny tagy ukazující na stejný digest. Explicitní vstupní tag zůstane primárním `tag`; u vstupu s digestem dostane přednost tag shodný s `release_id`, potom jediný jednoznačný nalezený tag. Ostatní shody se uloží do `extra_tags` konkrétní image. Každý `--extra-tag` se zaznamená pouze tehdy, když příkaz ověří, že už ukazuje na stejný digest. `--progress` zobrazí počet zkontrolovaných images, rychlost a ETA. Samostatná image zapíše skutečné `os/architecture`; OCI index se uzamkne digestem celého indexu a zapíše se jako `platform: multi-platform`. Pokud vstup kombinuje různé typy platforem, společná platforma na úrovni manifestu se vynechá.

### release selectors a compare

Stabilní identity `app_name/container_name`, které lze předat do `bundle publish --select`, vypíše:

```bash
# Tabulka pro člověka včetně detailů image
oci-toolbox release selectors releases/RE_2026.08.03.01.yml

# Jeden selector na řádek pro shell pipeline nebo generování selection souboru
oci-toolbox release selectors releases/RE_2026.08.03.01.yml --format plain

# Strukturovaný výstup s release_id a detaily images
oci-toolbox release selectors releases/RE_2026.08.03.01.yml --format json
```

Dva manifesty se porovnávají podle stejné identity. Porovnání je směrové z A do B a rozlišuje `same`, `changed`, `added` a `removed`. K dispozici jsou tři režimy:

- `--mode digest` je výchozí a porovnává pouze neměnné SHA digesty. Hodí se pro porovnání release vytvořeného jiným systémem s manifestem rekonstruovaným z clusteru.
- `--mode image` porovnává repository, digest a platformu. Tagy jsou pouze informativní, takže samotné přetagování novým release ID stav obsahu nezmění.
- `--mode full` porovnává všechna per-image release data: ID, source, repository, tag, digest, extra tags a platformu. Release ID a čas vytvoření na nejvyšší úrovni nejsou součástí identity image.

Ve všech režimech se řádky párují podle `app_name/container_name`; chybějící a nově přidané selectory jsou vždy rozdíl.

```bash
# Kompaktní tabulka digestů (výchozí režim)
oci-toolbox release compare releases/previous.yml releases/current.yml

# Jednořádkový výsledek
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode digest \
  --format summary

# Stabilní strojově zpracovatelný image report
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode image \
  --format json \
  --changed-only

# Striktní CI gate
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode full \
  --format summary \
  --fail-on-change
```

`--format summary` vypíše `MATCH` nebo `DIFFERENT` s počty. JSON navíc obsahuje `mode`, `matched_by` a boolean `match`. `--changed-only` filtruje pouze detailní řádky tabulky a JSON; souhrny vždy obsahují počty za celé porovnání.

Statusy v tabulce používají při výstupu do terminálu barvy ve stylu git diffu: `added` zeleně, `removed` červeně, `changed` žlutě a `same` tlumeně. Chování řídí `--color auto|always|never`; JSON nikdy neobsahuje ANSI escape sekvence a proměnná `NO_COLOR` automatické barvy vypne.

### release verify

Bez Kubernetes parametrů kontroluje `release verify` registry. Každý neměnný `image@digest` musí existovat a každý primární release tag zapsaný v manifestu musí stále ukazovat na tento digest:

```bash
oci-toolbox release verify releases/current.yml --progress

oci-toolbox release verify releases/current.yml \
  --creds "$REGISTRY_USER:$REGISTRY_PASS" \
  --insecure \
  --format json
```

Pohyblivé aliasy z `extra_tags`, například `stable`, se záměrně nekontrolují, protože po vydání novějšího release mohou legitimně ukazovat jinam.

S parametrem `--namespace` se manifest ověří proti běžným containerům Kubernetes Deploymentů. Aktuální image reference se přes registry přeloží na digest a porovnají podle `app_name/container_name`, repository, digestu a platformy, pokud je v manifestu uvedena. Chybějící a neočekávané selectory se hlásí jako drift. Rozdíl pouze v tagu se zaznamená jako `tag_changed`, ale shodný neměnný obsah zůstane ověřený.

```bash
oci-toolbox release verify releases/current.yml \
  --namespace example \
  --image-prefix registry.example.com/team \
  --kube-insecure \
  --insecure \
  --format json
```

Oba režimy vypíší úplný report a při nalezení driftu nebo chybě kontroly skončí nenulovým exit kódem, takže není potřeba další CI gate flag. Kubernetes TLS a registry TLS se řídí nezávisle pomocí `--kube-insecure` a `--insecure`.

Výchozí tabulka je záměrně kompaktní: u ověřených položek vypíše pouze status a selector, zatímco sloupec `DETAILS` se používá pro drift, chybu nebo rozdíl tagu. Úplné expected/actual hodnoty jsou dostupné přes `--format json`.

### Údržba registry

Údržba registry používá striktní YAML konfiguraci. Credentials neukládej do Gitu; předej je pomocí `--creds`, případně přes proměnné prostředí expandované ve starší kompatibilní konfiguraci.

```yaml
# oci-toolbox-registry.yml
registry:
  url: https://registry.example.com

cleanup:
  mode: created
  keep_count: 14
  protect_latest: true
  protect_tag_patterns:
    - "^stable$"

repositories:
  - name: team/api
  - name: team/ui
```

```bash
# Tagy, digesty, čas vytvoření a deduplikované velikosti.
oci-toolbox registry info \
  --config oci-toolbox-registry.yml \
  --creds user:password

# Repository dostupné přes registry catalog API.
oci-toolbox registry list-repositories \
  --config oci-toolbox-registry.yml

# Vygeneruje vstup pro: oci-toolbox copy --batch-file images.yml
oci-toolbox registry copy-plan \
  --config oci-toolbox-registry.yml \
  --dest-registry mirror.example.com \
  --output images.yml

# Bezpečný náhled, který nic nesmaže.
oci-toolbox registry cleanup --config oci-toolbox-registry.yml

# Provede vypsaný cleanup plán.
oci-toolbox registry cleanup --config oci-toolbox-registry.yml --apply
```

Cleanup pracuje s unikátními manifest digesty, takže více tagů stejné image nepočítá jako samostatné image. `mode: timestamp` řadí tagy odpovídající `YYYY.MM.DD.BUILD`; `mode: created` používá čas z image konfigurace. Chráněný tag zachová celý příslušný manifest digest. Registry příkazy podporují také `--insecure`, `--ca-cert`, `--timeout`, filtrování repository a ignorování chybějících repository.

### Snapshot Kubernetes images

`snapshot create` načte reference container a init-container images ze všech Deploymentů v Kubernetes namespace a uloží komprimované OCI image-layout archivy spolu s průběžně zapisovaným YAML manifestem. OCI archivy zachovají jak single-platform images, tak kompletní multi-platform image indexy. Přenos probíhá přímo přes vestavěný copy engine; není potřeba Docker daemon ani druhý proces `oci-toolbox`.

```bash
# Použije current-context z kubeconfigu.
oci-toolbox snapshot create \
  --namespace example \
  --output-dir ./snapshots \
  --src-creds user:password \
  --src-proxy http://proxy.example.com:8080

# Volitelný glob; * odpovídá i / a vyhodnocuje se nad celou image referencí.
oci-toolbox snapshot create \
  --namespace example \
  '*/team/docs-*:*'

# Obnoví každý archiv na původní image referenci.
oci-toolbox snapshot restore \
  --namespace example \
  --dest-creds user:password \
  --dest-proxy direct \
  ./snapshots/example/20260728-120000.yml
```

Manifest zůstává `complete: false`, dokud nejsou zapsány všechny archivy. Restore odmítne nekompletní manifest, jiný namespace, path traversal i archiv se změněnou velikostí nebo SHA-256 checksumem. Manifesty verze 2 používají OCI archivy; restore zůstává kompatibilní se staršími Docker archivy z manifestů verze 1. Zdroj zadaný digestem se obnoví přes deterministický tag `snapshot-<digest>`, takže původní digest reference začne být znovu dostupná. Pro Kubernetes přístup slouží `--kubeconfig`, `--context` a `--kube-insecure`; registry TLS má oddělené flagy `--src-insecure` a `--dest-insecure`.

---

## Git repository pro releases

Bundle deklarace a vygenerované release manifesty ukládej do samostatného Git repository s názvem `tsm-releases`:

```text
tsm-releases/
├── README.md
├── bundles/
│   ├── stable.yml
│   ├── candidate.yml
│   └── development.yml
└── releases/
    ├── RE_2026.06.25.01.yml
    └── RE_2026.07.29.01.yml
```

Mentální model je záměrně jednoduchý:

- `bundles/` říká, **co chceme vydat**. Jde o měnitelné deklarace spravované běžnými Git změnami a review.
- `releases/` říká, **co přesně bylo vydáno**. Soubory obsahují vyhodnocené image digesty a považují se za neměnné.
- Git historie říká, **kdo, co a kdy změnil nebo vygeneroval**.

`oci-toolbox` tyto YAML soubory čte a generuje, ale záměrně neprovádí Git commit, tag ani push. Tyto operace zůstávají úlohou CI/CD nebo release operátora.

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

**`shadow`** — Zabalí strom souborů do šifrované self-extracting binárky a původní cestu v image nahradí touto bootstrap binárkou. Výchozí chování nemění startovací metadata image. S `wrap-entrypoint: true` obalí původní `ENTRYPOINT`: před startem původního procesu spustí `/bin/sh` příkaz `app.self -cleanup`, který soubory obnoví a bootstrap smaže. Původní `ENTRYPOINT` i `CMD` zůstávají zachované. Bez `from:` čte z aktuální pracovní image, s `from:` může číst z jiné pojmenované source image a zapisovat do aktuální image. Pro wrapper musí cílová image obsahovat `/bin/sh`.

Nedávej `set-entrypoint` až za `shadow` s `wrap-entrypoint: true`, protože by wrapper přepsal. Vlastní `set-entrypoint` umísti před `shadow`. CLI flagy `--wrap-entrypoint` a `--no-wrap-entrypoint` přepíší všechny kroky v recipe a nelze je kombinovat. `--wrap-entrypoint` navíc před obalením převezme `Entrypoint` a `Cmd` z CLI image `SOURCE`; díky tomu zůstane při přepnutí na runtime image přes `use-base` zachovaný startovací příkaz aplikace.

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

```yaml
# Spustí app.self před původním Entrypoint a Cmd.
- action: shadow
  path: /app
  extract-to: /app
  output: /app/app.self
  wrap-entrypoint: true
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
oci-toolbox copy --recipe app.yml --reduce SOURCE DEST

# Původní/base vrstvy zůstanou, změny z recipe budou v jedné vrstvě navíc.
oci-toolbox copy --recipe app.yml --reduce-changes SOURCE DEST
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
    key: "org.opencontainers.image.patched-by"
    value: "oci-toolbox"
```

```bash
oci-toolbox copy \
  --src-creds user:pass --dest-creds user:pass \
  --recipe inject-encjson.yml \
  docker://registry.example.com/team/gateway:2.3-latest \
  docker://registry.example.com/team/gateway:2026-04-17.01
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
    key: "org.opencontainers.image.base.name"
    value: "alpine:3.21"
```

```bash
oci-toolbox copy \
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
    wrap-entrypoint: true

  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
```

```bash
oci-toolbox copy \
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

```bash
oci-toolbox copy \
  --recipe shadow-from-base.yml \
  --wrap-entrypoint \
  --reduce-changes \
  docker://registry.example.com/myapp:latest \
  docker://registry.example.com/myapp:shadowed
```

CLI flag je v tomto případě důležitý: obalí `Entrypoint` a `Cmd` z CLI source image, nikoliv startup metadata z `runtime-base`.

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
let service = SkopeoService::new("/usr/local/bin/oci-toolbox".to_string());
```

Všechny flagy (`--src-creds`, `--dest-creds`, `--debug`, `--dest-no-reuse`, `--creds`, `--override-arch`, `--override-os`) jsou identické.

---

## Spuštění testů

```bash
go test ./...
```

Testy jsou plně offline — nevyžadují přístup k žádnému registry. Používají in-memory OCI image sestavené z tar vrstev.

```
ok  github.com/martinmares/oci-toolbox/internal/image    (17 testů)
ok  github.com/martinmares/oci-toolbox/internal/recipe   (11 testů)
```
