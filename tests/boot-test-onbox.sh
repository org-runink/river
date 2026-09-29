#!/bin/bash
sudo pacman -Sy --noconfirm --needed qemu-system-x86 seabios socat >/tmp/qi.log 2>&1
ISO=$(find /os/iso-out -name '*.iso' | head -1)
mon() { echo "$1" | socat - unix-connect:/tmp/qmon 2>/dev/null; }
type_str() { local s="$1" i c k; for ((i=0;i<${#s};i++)); do c="${s:$i:1}"; case "$c" in
  " ") k=spc;; "/") k=slash;; ".") k=dot;; "-") k=minus;; "_") k="shift-minus";; *) k="$c";; esac
  mon "sendkey $k"; sleep 0.06; done; mon "sendkey ret"; }
qemu-system-x86_64 -enable-kvm -m 6144 -smp 4 -cdrom "$ISO" -boot d \
  -netdev user,id=n0 -device virtio-net,netdev=n0 -display none -vnc :4 \
  -serial file:/tmp/serial.log -monitor unix:/tmp/qmon,server,nowait &
QPID=$!
sleep 10; mon "sendkey down"; sleep 0.5; mon "sendkey down"; sleep 0.5; mon "sendkey ret"
echo "waiting for live boot..."; sleep 115
type_str "runink"; sleep 3          # login user
type_str "runink"; sleep 4          # password
type_str "sudo modprobe zfs"; sleep 3
type_str "runink"; sleep 4          # sudo password if prompted
type_str "zpool version"; sleep 3
type_str "zfs version"; sleep 3
mon "screendump /tmp/zfs.ppm"; sleep 3
[ -f /tmp/zfs.ppm ] && cp /tmp/zfs.ppm /os/build/zfs-shot.ppm && echo "zfs screenshot saved"
kill -9 $QPID 2>/dev/null; echo "BOOTTEST-DONE"
