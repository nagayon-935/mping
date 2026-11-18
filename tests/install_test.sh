#!/bin/sh
# Exercise the Linux installer with simulated privilege and file operations.
set -eu
installer=$(cd "$(dirname "$0")/.." && pwd)/install.sh
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cd "$scratch"
mkdir bin dest
touch mping
printf '#!/bin/sh\necho 0\n' >bin/id
printf '#!/bin/sh\necho Linux\n' >bin/uname
for tool in cp chmod chown setcap; do
  printf '#!/bin/sh\necho "%s $*" >>calls\n' "$tool" >"bin/$tool"
done
chmod +x bin/*
mv bin/setcap setcap
while read -r capability opt flag expected; do
  : >calls
  [ "$capability" = present ] && cp setcap bin/setcap
  set --
  [ "$flag" = yes ] && set -- --allow-setuid
  status=0
  PATH="$scratch/bin" INSTALL_DIR=dest MPING_ALLOW_SETUID="$opt" /bin/sh "$installer" "$@" >output 2>&1 || status=$?
  case $expected in
    reject)
      if [ "$status" = 0 ] || [ -s calls ]; then
        echo "expected refusal before installation (setcap=$capability opt=$opt flag=$flag)" >&2
        exit 1
      fi
      for hint in libcap2-bin libcap './install.sh'; do grep -Fw "$hint" output >/dev/null; done
      ;;
    setuid)
      [ "$status" = 0 ]
      grep -Fx 'chown root dest/mping' calls >/dev/null
      grep -Fx 'chmod u+s dest/mping' calls >/dev/null
      ;;
    setcap)
      [ "$status" = 0 ]
      grep -Fx 'setcap cap_net_raw+ep dest/mping' calls >/dev/null
      if grep -F 'chmod u+s' calls >/dev/null; then exit 1; fi
      ;;
  esac
  rm -f bin/setcap
done <<'CASES'
missing 0 no reject
missing yes no reject
missing 1 no setuid
missing 0 yes setuid
present 0 no setcap
present 1 yes setcap
CASES
