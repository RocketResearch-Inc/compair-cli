#!/usr/bin/env bash
set -euo pipefail

DIST_DIR=${1:?usage: build_linux_pages_repo.sh <dist-dir> <site-dir> <base-url>}
SITE_DIR=${2:?usage: build_linux_pages_repo.sh <dist-dir> <site-dir> <base-url>}
BASE_URL=${3:?usage: build_linux_pages_repo.sh <dist-dir> <site-dir> <base-url>}

PACKAGE_NAME=${PACKAGE_NAME:-compair}
GPG_PASSPHRASE=${LINUX_REPO_GPG_PASSPHRASE:?LINUX_REPO_GPG_PASSPHRASE is required}

if ! command -v dpkg-scanpackages >/dev/null 2>&1; then
  echo "dpkg-scanpackages is required" >&2
  exit 1
fi

if ! command -v apt-ftparchive >/dev/null 2>&1; then
  echo "apt-ftparchive is required" >&2
  exit 1
fi

if ! command -v createrepo_c >/dev/null 2>&1; then
  echo "createrepo_c is required" >&2
  exit 1
fi

if ! command -v rpm >/dev/null 2>&1; then
  echo "rpm is required" >&2
  exit 1
fi

if ! command -v gpg >/dev/null 2>&1; then
  echo "gpg is required" >&2
  exit 1
fi

GPG_KEY_ID=${LINUX_REPO_GPG_KEY_ID:-}
if [[ -z "$GPG_KEY_ID" ]]; then
  GPG_KEY_ID=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ { print $10; exit }')
fi

if [[ -z "$GPG_KEY_ID" ]]; then
  echo "could not determine a GPG secret key fingerprint" >&2
  exit 1
fi

APT_ROOT="$SITE_DIR/apt"
APT_POOL="$APT_ROOT/pool/main/c/$PACKAGE_NAME"
RPM_ROOT="$SITE_DIR/rpm"
INSTALL_ROOT="$SITE_DIR/install"

mkdir -p \
  "$APT_POOL" \
  "$APT_ROOT/dists/stable/main/binary-amd64" \
  "$APT_ROOT/dists/stable/main/binary-arm64" \
  "$RPM_ROOT/x86_64" \
  "$RPM_ROOT/aarch64" \
  "$INSTALL_ROOT"

find "$DIST_DIR" -maxdepth 1 -type f -name '*.deb' -print0 | while IFS= read -r -d '' file; do
  cp -f "$file" "$APT_POOL/"
done

find "$DIST_DIR" -maxdepth 1 -type f -name '*.rpm' -print0 | while IFS= read -r -d '' file; do
  arch=$(rpm -qp --qf '%{ARCH}\n' "$file")
  case "$arch" in
    x86_64|aarch64)
      mkdir -p "$RPM_ROOT/$arch"
      cp -f "$file" "$RPM_ROOT/$arch/"
      ;;
    *)
      echo "skipping rpm with unsupported arch: $file ($arch)" >&2
      ;;
  esac
done

for arch in amd64 arm64; do
  packages_dir="$APT_ROOT/dists/stable/main/binary-$arch"
  dpkg-scanpackages -a "$arch" "$APT_POOL" /dev/null > "$packages_dir/Packages"
  gzip -9c "$packages_dir/Packages" > "$packages_dir/Packages.gz"
done

cat > "$APT_ROOT/apt-ftparchive.conf" <<EOF
APT::FTPArchive::Release::Origin "Rocket Research, Inc.";
APT::FTPArchive::Release::Label "Compair";
APT::FTPArchive::Release::Suite "stable";
APT::FTPArchive::Release::Codename "stable";
APT::FTPArchive::Release::Architectures "amd64 arm64";
APT::FTPArchive::Release::Components "main";
APT::FTPArchive::Release::Description "Compair CLI Debian repository";
EOF

(
  cd "$APT_ROOT"
  apt-ftparchive -c apt-ftparchive.conf release dists/stable > dists/stable/Release
)

gpg --batch --yes --pinentry-mode loopback --passphrase "$GPG_PASSPHRASE" \
  --local-user "$GPG_KEY_ID" --armor --detach-sign \
  --output "$APT_ROOT/dists/stable/Release.gpg" "$APT_ROOT/dists/stable/Release"
gpg --batch --yes --pinentry-mode loopback --passphrase "$GPG_PASSPHRASE" \
  --local-user "$GPG_KEY_ID" --clearsign \
  --output "$APT_ROOT/dists/stable/InRelease" "$APT_ROOT/dists/stable/Release"

for arch in x86_64 aarch64; do
  rpm_dir="$RPM_ROOT/$arch"
  createrepo_c --update "$rpm_dir"
  gpg --batch --yes --pinentry-mode loopback --passphrase "$GPG_PASSPHRASE" \
    --local-user "$GPG_KEY_ID" --armor --detach-sign \
    --output "$rpm_dir/repodata/repomd.xml.asc" "$rpm_dir/repodata/repomd.xml"
done

gpg --batch --yes --armor --export "$GPG_KEY_ID" > "$SITE_DIR/gpg.key"

cat > "$INSTALL_ROOT/compair.repo" <<EOF
[compair]
name=Compair Packages
baseurl=$BASE_URL/rpm/\$basearch
enabled=1
gpgcheck=0
repo_gpgcheck=1
gpgkey=$BASE_URL/gpg.key
EOF

cat > "$INSTALL_ROOT/debian.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
curl -fsSL '$BASE_URL/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/compair-archive-keyring.gpg
echo 'deb [signed-by=/usr/share/keyrings/compair-archive-keyring.gpg] $BASE_URL/apt stable main' | sudo tee /etc/apt/sources.list.d/compair.list >/dev/null
sudo apt update
sudo apt install compair
EOF
chmod +x "$INSTALL_ROOT/debian.sh"

cat > "$SITE_DIR/index.html" <<EOF
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Compair Linux Packages</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
      :root {
        color-scheme: light;
        --bg: #f4f1e8;
        --fg: #1f2421;
        --accent: #315c4b;
        --card: #fffdf8;
        --muted: #6b726e;
      }
      body {
        margin: 0;
        padding: 3rem 1.5rem;
        font-family: Georgia, "Times New Roman", serif;
        background: radial-gradient(circle at top, #fff7dc, var(--bg));
        color: var(--fg);
      }
      main {
        max-width: 52rem;
        margin: 0 auto;
        background: var(--card);
        border: 1px solid #d8d0be;
        border-radius: 1rem;
        padding: 2rem;
        box-shadow: 0 18px 40px rgba(30, 32, 31, 0.08);
      }
      h1, h2 {
        margin-top: 0;
      }
      pre {
        overflow-x: auto;
        padding: 1rem;
        border-radius: 0.75rem;
        background: #161b19;
        color: #f6f7f2;
      }
      code {
        font-family: "SFMono-Regular", "Consolas", monospace;
      }
      a {
        color: var(--accent);
      }
      .muted {
        color: var(--muted);
      }
    </style>
  </head>
  <body>
    <main>
      <h1>Compair Linux Packages</h1>
      <p class="muted">APT and RPM repositories for Compair CLI.</p>

      <h2>Debian / Ubuntu</h2>
      <pre><code>curl -fsSL '$BASE_URL/install/debian.sh' | bash</code></pre>

      <h2>Fedora / RHEL</h2>
      <pre><code>curl -fsSL '$BASE_URL/install/compair.repo' | sudo tee /etc/yum.repos.d/compair.repo >/dev/null
sudo dnf install compair</code></pre>

      <h2>Repository files</h2>
      <ul>
        <li><a href="$BASE_URL/gpg.key">GPG public key</a></li>
        <li><a href="$BASE_URL/install/compair.repo">RPM repo file</a></li>
        <li><a href="$BASE_URL/apt/dists/stable/InRelease">APT InRelease</a></li>
      </ul>
    </main>
  </body>
</html>
EOF
