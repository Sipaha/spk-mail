#!/usr/bin/python3
"""Isolated Wails/WebKitGTK runner. Never uses the inherited desktop or mailbox."""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import tempfile
import time
import urllib.request
from PIL import ImageGrab

parser = argparse.ArgumentParser()
parser.add_argument('--scratch', type=Path, required=True)
parser.add_argument('--binary', type=Path, required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parents[2]
args.scratch.mkdir(parents=True, exist_ok=True)
work = Path(tempfile.mkdtemp(prefix='message-body-native-', dir=args.scratch))
env = os.environ.copy()
assert env.get('TMPDIR'), 'Preserve host-assigned TMPDIR'
for name in ['DISPLAY', 'WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS', 'WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS']:
    env.pop(name, None)
for name in ['HOME', 'XDG_DATA_HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME']:
    path = work / name
    path.mkdir(mode=0o700)
    env[name] = str(path)
runtime = Path(tempfile.mkdtemp(prefix='mb-', dir=env['TMPDIR']))
runtime.chmod(0o700)
env['XDG_RUNTIME_DIR'] = str(runtime)
env['GTK_A11Y'] = 'none'
env['NO_PROXY'] = env['no_proxy'] = '127.0.0.1,localhost'
# Capture the actual native Browser.OpenURL destination without opening an
# operator browser or making a network request.
bin_dir = work / 'bin'
bin_dir.mkdir()
link_log = work / 'opened-urls.txt'
xdg = bin_dir / 'xdg-open'
xdg.write_text('#!/bin/sh\nprintf "%s\\n" "$1" >> "$MAIL_BODY_LINK_LOG"\n')
xdg.chmod(0o700)
env['MAIL_BODY_LINK_LOG'] = str(link_log)
env['PATH'] = str(bin_dir) + os.pathsep + env['PATH']
http = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def get(path):
    with http.open('http://127.0.0.1:5186' + path, timeout=2) as response:
        return response.read()

processes = []
try:
    server = subprocess.Popen([str(repo / 'frontend/node_modules/.bin/vite'), '--config', 'frontend/message-body.vite.config.ts'], cwd=repo, env=env, stdout=open(work / 'server.log', 'w'), stderr=subprocess.STDOUT, start_new_session=True)
    processes.append(server)
    for _ in range(100):
        if server.poll() is not None:
            raise RuntimeError((work / 'server.log').read_text())
        try:
            get('/tests/message-body/')
            break
        except OSError:
            time.sleep(.1)
    else:
        raise RuntimeError('Fixture server did not start')
    r, w = os.pipe()
    xvfb = subprocess.Popen(['Xvfb', '-displayfd', str(w), '-screen', '0', '1000x800x24', '-nolisten', 'tcp'], pass_fds=(w,), stdout=open(work / 'xvfb.log', 'w'), stderr=subprocess.STDOUT, start_new_session=True)
    processes.append(xvfb)
    os.close(w)
    assert select.select([r], [], [], 5)[0], 'Xvfb did not provide a display'
    with os.fdopen(r) as display_pipe:
        display_number = display_pipe.readline().strip()
    assert display_number.isdecimal(), 'Invalid owned Xvfb display'
    env['DISPLAY'] = ':' + display_number
    conf = work / 'bus.conf'
    conf.write_text('<busconfig><type>session</type><listen>unix:abstract=mail-' + work.name + '</listen><auth>EXTERNAL</auth><policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy></busconfig>')
    app = subprocess.Popen(['dbus-run-session', '--config-file', str(conf), '--', str(args.binary.resolve())], env=env, stdout=open(work / 'app.log', 'w'), stderr=subprocess.STDOUT, start_new_session=True)
    processes.append(app)
    seen = set()
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        assert app.poll() is None, (work / 'app.log').read_text()
        report = json.loads(get('/report'))
        if not report or report['stage'] in seen:
            time.sleep(.05)
            continue
        stage = report['stage']
        seen.add(stage)
        (work / (stage + '.json')).write_text(json.dumps(report, indent=2))
        time.sleep(.1)  # Let the checkpoint's already prepared frame paint.
        ImageGrab.grab(xdisplay=env['DISPLAY']).save(work / (stage + '.png'))
        if stage in ['native-link', 'native-wheel']:
            # Native input goes only to this runner's private Xvfb display.
            xlib = ctypes.CDLL('libX11.so.6')
            xtst = ctypes.CDLL('libXtst.so.6')
            xlib.XOpenDisplay.restype = ctypes.c_void_p
            xlib.XOpenDisplay.argtypes = [ctypes.c_char_p]
            xlib.XFlush.argtypes = [ctypes.c_void_p]
            xlib.XCloseDisplay.argtypes = [ctypes.c_void_p]
            xtst.XTestFakeMotionEvent.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_ulong]
            xtst.XTestFakeButtonEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
            display = xlib.XOpenDisplay(env['DISPLAY'].encode())
            assert display, 'Cannot open owned display'
            target = report['evidence']
            xtst.XTestFakeMotionEvent(display, 0, round(target['x'] * 1.1), round(target['y'] * 1.1), 0)
            button = 1 if stage == 'native-link' else 5
            xtst.XTestFakeButtonEvent(display, button, 1, 0)
            xtst.XTestFakeButtonEvent(display, button, 0, 0)
            xlib.XFlush(display)
            time.sleep(.15)
            xlib.XCloseDisplay(display)
        if stage == 'native-link':
            for _ in range(50):
                if link_log.exists():
                    break
                time.sleep(.02)
            assert link_log.read_text().splitlines() == ['https://example.test/link'], 'Native URL must be routed exactly once'
        get('/ack')
        if stage == 'failed':
            raise RuntimeError(report)
        if stage == 'passed':
            result = {'status': 'passed', 'stages': sorted(seen), 'binarySHA256': hashlib.sha256(args.binary.read_bytes()).hexdigest(), 'sourceSHA256': hashlib.sha256((repo / 'frontend/src/components/MessageBody.tsx').read_bytes()).hexdigest(), 'sandboxDisableRequested': False}
            (work / 'result.json').write_text(json.dumps(result, indent=2))
            print(json.dumps(result))
            print('Artifacts: ' + str(work))
            break
    else:
        ImageGrab.grab(xdisplay=env['DISPLAY']).save(work / 'timeout.png')
        raise RuntimeError('Native checkpoint timed out; artifacts: ' + str(work))
finally:
    for process in reversed(processes):
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
    shutil.rmtree(runtime)
