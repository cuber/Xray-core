#!/usr/bin/env python3
"""Opt-in isolated executable baseline/interop; never touches installed services."""
import argparse
import base64
import contextlib
import hashlib
import json
import os
from pathlib import Path
import socket
import socketserver
import struct
import subprocess
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parent
ART = ROOT / "safety-artifacts"
TOOLS = Path('/Volumes/Linux/opensource/cuber/xray-config/.cache/anytls/tools')
BINS = {name: ART / ('xray-' + name) for name in ('old', 'pristine', 'candidate')}
BINS.update({'sing-box': TOOLS / 'sing-box-1.14.2-darwin-arm64/sing-box',
             'mihomo': TOOLS / 'mihomo'})


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def exact(s, size):
    result = b''
    while len(result) < size:
        part = s.recv(size - len(result))
        if not part:
            raise EOFError('short stream')
        result += part
    return result


def base():
    return {'log': {'loglevel': 'error'}, 'policy': {'levels': {'0': {
        'handshake': 1, 'connIdle': 10, 'uplinkOnly': 1, 'downlinkOnly': 1}}}}


def server_config(name, p, method, multi=False, relay=False):
    n = 16 if '128' in method else 32
    master = base64.b64encode(bytes(range(n))).decode()
    user = base64.b64encode(bytes(range(32, 32+n))).decode()
    settings = {'method': method, 'password': master, 'network': 'tcp,udp'}
    if multi or relay:
        client = {'password': user, 'email': 'fixture', 'level': 0}
        if relay:
            client.update(address='127.0.0.1', port=9)
        settings['clients'] = [client]
    cfg = base()
    cfg['inbounds'] = [{'listen': '127.0.0.1', 'port': p,
                        'protocol': 'shadowsocks', 'settings': settings}]
    freedom = {'ipsBlocked': []} if name == 'old' else {
        'finalRules': [{'action': 'allow', 'ip': ['127.0.0.0/8']}]}
    cfg['outbounds'] = [{'protocol': 'freedom', 'settings': freedom}]
    return cfg, master + ':' + user if multi else master


def client_config(name, p, sp, method, key):
    if name == 'sing-box':
        return {'log': {'level': 'error'}, 'inbounds': [{'type': 'socks',
            'listen': '127.0.0.1', 'listen_port': p}], 'outbounds': [{
            'type': 'shadowsocks', 'tag': 'proxy', 'server': '127.0.0.1',
            'server_port': sp, 'method': method, 'password': key}],
            'route': {'final': 'proxy'}}
    if name == 'mihomo':
        return {'socks-port': p, 'allow-lan': False, 'bind-address': '127.0.0.1',
            'mode': 'rule', 'log-level': 'error', 'proxies': [{'name': 'ss',
            'type': 'ss', 'server': '127.0.0.1', 'port': sp, 'cipher': method,
            'password': key, 'udp': True}], 'rules': ['MATCH,ss']}
    cfg = base()
    cfg['inbounds'] = [{'listen': '127.0.0.1', 'port': p, 'protocol': 'socks',
        'settings': {'auth': 'noauth', 'udp': True, 'ip': '127.0.0.1'}}]
    cfg['outbounds'] = [{'protocol': 'shadowsocks', 'settings': {'servers': [{
        'address': '127.0.0.1', 'port': sp, 'method': method, 'password': key}]}}]
    return cfg


@contextlib.contextmanager
def process(name, cfg, p):
    with tempfile.TemporaryDirectory(dir=ART) as directory:
        path = Path(directory) / 'config.json'
        path.write_text(json.dumps(cfg))
        args = [str(BINS[name])]
        args += (['run', '-c', str(path)] if name == 'sing-box' else
                 ['-f', str(path), '-d', directory] if name == 'mihomo' else
                 ['run', '-config', str(path)])
        with open(Path(directory) / 'log', 'w+') as log:
            proc = subprocess.Popen(args, stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 10
                while True:
                    if proc.poll() is not None:
                        raise RuntimeError('process exited at startup')
                    try:
                        with socket.create_connection(('127.0.0.1', p), .1):
                            break
                    except OSError:
                        if time.monotonic() >= deadline:
                            raise TimeoutError('listener startup')
                        time.sleep(.05)
                yield proc
            except Exception:
                log.flush()
                log.seek(0)
                print(log.read(), flush=True)
                raise
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()


def fdcount(pid):
    if os.uname().sysname == 'Linux':
        return len(os.listdir(f'/proc/{pid}/fd'))
    output = subprocess.check_output(['lsof', '-nP', '-a', '-p', str(pid), '-F', 'f'], text=True)
    return sum(line.startswith('f') and line[1:].isdigit() for line in output.splitlines())


def baseline(name, kind):
    p = port()
    cfg, _ = server_config(name, p, '2022-blake3-aes-128-gcm',
                           kind == 'multi', kind == 'relay')
    samples, held = [], []
    with process(name, cfg, p) as proc:
        time.sleep(.3)
        initial = fdcount(proc.pid)
        try:
            for wave in range(10):
                for _ in range(20):
                    s = socket.create_connection(('127.0.0.1', p), 3)
                    s.setblocking(False)
                    held.append(s)
                time.sleep(1.5)
                live = []
                for s in held:
                    try:
                        data = s.recv(1, socket.MSG_PEEK)
                        assert not data, 'unexpected server data'
                        s.close()
                    except BlockingIOError:
                        live.append(s)
                    except ConnectionResetError:
                        s.close()
                held = live
                sample = {'wave': wave, 'server_fd': fdcount(proc.pid), 'still_open': len(held)}
                samples.append(sample)
                print(json.dumps({'baseline': name, 'kind': kind, **sample}), flush=True)
        finally:
            for s in held:
                s.close()
        time.sleep(3)
        return {'binary': name, 'kind': kind, 'baseline_fd': initial,
                'samples': samples, 'after_peer_close_fd': fdcount(proc.pid)}


class TCPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.settimeout(10)
        while True:
            data = self.request.recv(65536)
            if not data:
                return
            self.request.sendall(data)


class UDPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)


@contextlib.contextmanager
def echoes():
    servers = [socketserver.ThreadingTCPServer(('127.0.0.1', 0), TCPHandler)]
    servers += [socketserver.UDPServer(('127.0.0.1', 0), UDPHandler) for _ in range(2)]
    threads = [threading.Thread(target=s.serve_forever) for s in servers]
    for t in threads:
        t.start()
    try:
        yield [s.server_address[1] for s in servers]
    finally:
        for s in servers:
            s.shutdown()
            s.server_close()
        for t in threads:
            t.join()


def socks(p, command, target):
    s = socket.create_connection(('127.0.0.1', p), 5)
    try:
        s.sendall(b'\x05\x01\x00')
        assert exact(s, 2) == b'\x05\x00'
        s.sendall(bytes([5, command, 0, 1]) + socket.inet_aton('127.0.0.1') + struct.pack('!H', target))
        header = exact(s, 4)
        assert header[:3] == b'\x05\x00\x00', header
        if header[3] == 1:
            address = socket.inet_ntoa(exact(s, 4))
        elif header[3] == 4:
            address = socket.inet_ntop(socket.AF_INET6, exact(s, 16))
        else:
            address = exact(s, exact(s, 1)[0]).decode()
        return s, (address, struct.unpack('!H', exact(s, 2))[0])
    except Exception:
        s.close()
        raise


def interop(client, server, method, multi, targets):
    sp, cp = port(), port()
    cfg, key = server_config(server, sp, method, multi)
    with process(server, cfg, sp), process(client, client_config(client, cp, sp, method, key), cp):
        s, _ = socks(cp, 1, targets[0])
        tcp_bytes = 0
        with s:
            for index, size in enumerate((64, 131072)):
                if index:
                    time.sleep(3)
                payload = bytes(range(256)) * (size // 256) + b'x' * (size % 256)
                s.sendall(payload)
                assert exact(s, size) == payload
                tcp_bytes += size
        control, endpoint = socks(cp, 3, 0)
        udp_bytes = 0
        with control, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
            udp.settimeout(5)
            for index in range(12):
                target = targets[1 + index % 2]
                header = b'\x00\x00\x00\x01' + socket.inet_aton('127.0.0.1') + struct.pack('!H', target)
                payload = bytes([index]) * (31, 997, 1180)[index % 3]
                udp.sendto(header + payload, endpoint)
                received, _ = udp.recvfrom(65535)
                assert received == header + payload, (len(received), len(payload))
                udp_bytes += len(payload)
        return {'tcp_bytes_each_direction': tcp_bytes, 'udp_bytes_each_direction': udp_bytes,
                'udp_packets': 12, 'late_tcp_delay_seconds': 3}


def duplicate_keys(name, reverse, distinct, target, remove=False):
    sp, cp = port(), port()
    method = '2022-blake3-aes-128-gcm'
    cfg, key = server_config(name, sp, method, True)
    settings = cfg['inbounds'][0]['settings']
    first = dict(settings['clients'][0], email='admin')
    second = dict(first, email='la.akile.lite')
    if distinct:
        second['password'] = base64.b64encode(bytes(range(64, 80))).decode()
    settings['clients'] = [second, first] if reverse else [first, second]
    if remove:
        api_port = port()
        cfg['api'] = {'tag': 'api', 'services': ['HandlerService']}
        cfg['inbounds'][0]['tag'] = 'ss'
        cfg['inbounds'].append({'tag': 'api', 'listen': '127.0.0.1', 'port': api_port,
            'protocol': 'dokodemo-door', 'settings': {'address': '127.0.0.1'}})
        cfg['routing'] = {'rules': [{'type': 'field', 'inboundTag': ['api'], 'outboundTag': 'api'}]}
    with tempfile.TemporaryDirectory(dir=ART) as directory:
        access = Path(directory) / 'access.log'
        cfg['log']['access'] = str(access)
        config_path = Path(directory) / 'server.json'
        config_path.write_text(json.dumps(cfg))
        check = subprocess.run([str(BINS[name]), 'run', '-test', '-config', str(config_path)],
                               capture_output=True, text=True)
        result = {'binary': name, 'reverse_order': reverse, 'distinct_keys': distinct,
                  'remove_admin': remove, 'config_exit': check.returncode}
        if check.returncode:
            result['config_output'] = check.stdout + check.stderr
            assert name == 'candidate' and not distinct
            assert 'key already exists' in result['config_output']
            return result
        with process(name, cfg, sp):
            for user in ([first, second] if distinct else [first]):
                client_key = settings['password'] + ':' + user['password']
                with process('candidate', client_config('candidate', cp, sp, method, client_key), cp):
                    s, _ = socks(cp, 1, target)
                    with s:
                        s.sendall(b'duplicate-key-identity')
                        assert exact(s, 22) == b'duplicate-key-identity'
                time.sleep(.1)
            if remove:
                removal = subprocess.run([str(BINS['old']), 'api', 'rmu',
                    '--server=127.0.0.1:' + str(api_port), '-tag=ss', 'admin'],
                    capture_output=True, text=True, timeout=10)
                assert removal.returncode == 0, removal.stdout + removal.stderr
                client_key = settings['password'] + ':' + second['password']
                with process('candidate', client_config('candidate', cp, sp, method, client_key), cp):
                    try:
                        s, _ = socks(cp, 1, target)
                        with s:
                            s.sendall(b'after-removal')
                            result['remaining_user_works'] = exact(s, 13) == b'after-removal'
                    except (OSError, EOFError):
                        result['remaining_user_works'] = False
                assert result['remaining_user_works'] == (distinct or name == 'old'), result
        result['access'] = access.read_text()
        if distinct:
            assert 'email: admin' in result['access'] and 'email: la.akile.lite' in result['access']
        else:
            selected = 'admin' if reverse else 'la.akile.lite'
            assert 'email: ' + selected in result['access'], result
            result['selected_email'] = selected
        return result


def main():
    global ART
    parser = argparse.ArgumentParser()
    parser.add_argument('--mode', choices=['baseline', 'interop', 'duplicates'], required=True)
    parser.add_argument('--binary-dir', type=Path)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--work-dir', type=Path)
    parser.add_argument('--core-only', action='store_true')
    args = parser.parse_args()
    if args.work_dir:
        ART = args.work_dir
    if args.binary_dir:
        for name in ('old', 'pristine', 'candidate'):
            BINS[name] = args.binary_dir / ('xray-' + name)
    ART.mkdir(exist_ok=True)
    report = {'mode': args.mode, 'platform': list(os.uname()), 'binaries': {}, 'results': []}
    for name, path in BINS.items():
        if (args.mode != 'interop' or args.core_only) and name in ('sing-box', 'mihomo'):
            continue
        version = subprocess.check_output([str(path), '-v' if name == 'mihomo' else 'version'], text=True)
        report['binaries'][name] = {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'version': version}
    output = args.report or ROOT / ('safety-' + args.mode + '-20261014.json')
    def record(result):
        report['results'].append(result)
        output.write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(result), flush=True)
    if args.mode == 'baseline':
        for name in ('old', 'pristine', 'candidate'):
            for kind in ('single', 'multi', 'relay'):
                result = baseline(name, kind)
                record(result)
                for index, sample in enumerate(result['samples']):
                    retained = 20 * (index + 1) if name == 'old' else 0
                    assert sample['still_open'] == retained, result
                    assert abs(sample['server_fd'] - result['baseline_fd'] - retained) <= 2, result
                assert result['after_peer_close_fd'] <= result['baseline_fd'] + 2, result
    elif args.mode == 'duplicates':
        with echoes() as targets:
            for name in ('old', 'pristine', 'candidate'):
                for reverse, distinct in ((False, False), (True, False), (False, True)):
                    record(duplicate_keys(name, reverse, distinct, targets[0]))
                record(duplicate_keys(name, False, name == 'candidate', targets[0], remove=True))
    else:
        with echoes() as targets:
            pairs = [('old', 'candidate'), ('candidate', 'old')]
            if not args.core_only:
                pairs += [('sing-box', 'candidate'), ('mihomo', 'candidate')]
            for client, server in pairs:
                for method in ('2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm',
                               '2022-blake3-chacha20-poly1305'):
                    for multi in ([False] if 'chacha' in method else [False, True]):
                        result = {'client': client, 'server': server, 'method': method, 'multi': multi}
                        try:
                            result.update(interop(client, server, method, multi, targets), passed=True)
                        except Exception as error:
                            result.update(passed=False, error=repr(error))
                        record(result)
        if any(not r['passed'] for r in report['results']):
            raise SystemExit(1)


if __name__ == '__main__':
    main()
