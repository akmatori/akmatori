"""
SSH Command Execution Module

Provides parallel SSH command execution across multiple servers with
secure key handling and aggregated results.

Example:
    from ssh import execute_command, test_connectivity

    # Execute command on all configured servers
    result = execute_command("uptime")

    # Test connectivity
    connectivity = test_connectivity()
"""

import json
import os
import stat
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import List, Optional, Dict, Any

import paramiko

from .config import (
    get_private_key,
    get_servers,
    get_username,
    get_port,
    get_command_timeout,
    get_connection_timeout,
    get_known_hosts_policy,
    debug_log,
)


class SSHKeyManager:
    """Context manager for secure temporary key file handling"""

    def __init__(self, key_content: str):
        self.key_content = key_content
        self.key_path: Optional[str] = None

    def __enter__(self) -> str:
        """Create temporary key file with secure permissions"""
        fd, self.key_path = tempfile.mkstemp(prefix='ssh_key_', suffix='.pem')
        try:
            os.write(fd, self.key_content.encode('utf-8'))
            if not self.key_content.endswith('\n'):
                os.write(fd, b'\n')
        finally:
            os.close(fd)

        os.chmod(self.key_path, stat.S_IRUSR | stat.S_IWUSR)
        debug_log(f"Created temporary key file: {self.key_path}")
        return self.key_path

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Securely remove temporary key file"""
        if self.key_path and os.path.exists(self.key_path):
            try:
                with open(self.key_path, 'wb') as f:
                    f.write(b'\x00' * os.path.getsize(self.key_path))
                os.remove(self.key_path)
                debug_log(f"Removed temporary key file: {self.key_path}")
            except Exception as e:
                debug_log(f"Warning: Failed to remove key file: {e}")
        return False


def _get_host_key_policy() -> paramiko.MissingHostKeyPolicy:
    """Get the appropriate host key policy based on configuration"""
    policy = get_known_hosts_policy()
    if policy == 'strict':
        return paramiko.RejectPolicy()
    elif policy == 'auto_add':
        return paramiko.AutoAddPolicy()
    else:
        return paramiko.WarningPolicy()


def _load_private_key(key_path: str) -> paramiko.PKey:
    """Load private key from file, trying different key types"""
    # Build list of available key types (DSSKey removed in paramiko 4.x)
    key_types = [
        paramiko.RSAKey,
        paramiko.Ed25519Key,
        paramiko.ECDSAKey,
    ]
    # Add DSSKey if available (paramiko < 4.0)
    if hasattr(paramiko, 'DSSKey'):
        key_types.append(paramiko.DSSKey)

    last_error = None
    for key_class in key_types:
        try:
            return key_class.from_private_key_file(key_path)
        except Exception as e:
            last_error = e
            continue

    raise ValueError(f"Unable to load private key: {last_error}")


def _execute_on_single_server(
    server: str,
    command: str,
    key_path: str,
    username: str,
    port: int,
    command_timeout: int,
    connection_timeout: int,
) -> Dict[str, Any]:
    """Execute command on a single server"""
    start_time = time.time()

    try:
        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(_get_host_key_policy())

        private_key = _load_private_key(key_path)

        debug_log(f"Connecting to {server}:{port} as {username}")

        client.connect(
            hostname=server,
            port=port,
            username=username,
            pkey=private_key,
            timeout=connection_timeout,
            allow_agent=False,
            look_for_keys=False,
        )

        debug_log(f"Connected to {server}, executing command: {command}")

        stdin, stdout, stderr = client.exec_command(
            command,
            timeout=command_timeout,
        )

        exit_code = stdout.channel.recv_exit_status()
        stdout_text = stdout.read().decode('utf-8', errors='replace')
        stderr_text = stderr.read().decode('utf-8', errors='replace')

        duration_ms = int((time.time() - start_time) * 1000)

        client.close()

        debug_log(f"Command completed on {server}: exit_code={exit_code}")

        return {
            'server': server,
            'success': exit_code == 0,
            'stdout': stdout_text,
            'stderr': stderr_text,
            'exit_code': exit_code,
            'duration_ms': duration_ms,
            'error': None,
        }

    except paramiko.AuthenticationException as e:
        duration_ms = int((time.time() - start_time) * 1000)
        return {
            'server': server,
            'success': False,
            'stdout': '',
            'stderr': '',
            'exit_code': -1,
            'duration_ms': duration_ms,
            'error': f"Authentication failed: {str(e)}",
        }
    except paramiko.SSHException as e:
        duration_ms = int((time.time() - start_time) * 1000)
        return {
            'server': server,
            'success': False,
            'stdout': '',
            'stderr': '',
            'exit_code': -1,
            'duration_ms': duration_ms,
            'error': f"SSH error: {str(e)}",
        }
    except TimeoutError as e:
        duration_ms = int((time.time() - start_time) * 1000)
        return {
            'server': server,
            'success': False,
            'stdout': '',
            'stderr': '',
            'exit_code': -1,
            'duration_ms': duration_ms,
            'error': f"Connection timeout: {str(e)}",
        }
    except Exception as e:
        duration_ms = int((time.time() - start_time) * 1000)
        return {
            'server': server,
            'success': False,
            'stdout': '',
            'stderr': '',
            'exit_code': -1,
            'duration_ms': duration_ms,
            'error': f"Error: {str(e)}",
        }


def execute_command(command: str) -> str:
    """
    Execute a command on all configured servers in parallel.

    Args:
        command: The shell command to execute on remote servers

    Returns:
        JSON string with per-server results and summary

    Example:
        result = execute_command("df -h")
        data = json.loads(result)
        for server_result in data['results']:
            print(f"{server_result['server']}: {server_result['stdout']}")
    """
    servers = get_servers()
    if not servers:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'succeeded': 0, 'failed': 0},
            'error': 'No servers configured'
        })

    return _execute_on_servers_internal(command, servers)


def execute_on_servers(command: str, servers: List[str]) -> str:
    """
    Execute a command on a specific subset of servers.

    Args:
        command: The shell command to execute
        servers: List of server hostnames/IPs to target

    Returns:
        JSON string with per-server results and summary
    """
    if not servers:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'succeeded': 0, 'failed': 0},
            'error': 'No servers specified'
        })

    configured_servers = set(get_servers())
    invalid_servers = [s for s in servers if s not in configured_servers]
    if invalid_servers:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'succeeded': 0, 'failed': 0},
            'error': f"Invalid servers (not configured): {', '.join(invalid_servers)}"
        })

    return _execute_on_servers_internal(command, servers)


def _execute_on_servers_internal(command: str, servers: List[str]) -> str:
    """Internal implementation for parallel command execution"""
    private_key = get_private_key()
    if not private_key:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'succeeded': 0, 'failed': 0},
            'error': 'SSH private key not configured'
        })

    username = get_username()
    if not username:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'succeeded': 0, 'failed': 0},
            'error': 'SSH username not configured'
        })

    port = get_port()
    command_timeout = get_command_timeout()
    connection_timeout = get_connection_timeout()

    results: List[Dict[str, Any]] = []

    with SSHKeyManager(private_key) as key_path:
        max_workers = min(len(servers), 10)

        with ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = {
                executor.submit(
                    _execute_on_single_server,
                    server,
                    command,
                    key_path,
                    username,
                    port,
                    command_timeout,
                    connection_timeout,
                ): server
                for server in servers
            }

            for future in as_completed(futures):
                result = future.result()
                results.append(result)

    results.sort(key=lambda r: r['server'])

    succeeded = sum(1 for r in results if r['success'])
    failed = len(results) - succeeded

    return json.dumps({
        'results': results,
        'summary': {
            'total': len(results),
            'succeeded': succeeded,
            'failed': failed,
        }
    })


def test_connectivity() -> str:
    """
    Test SSH connectivity to all configured servers.

    Returns:
        JSON string with connectivity status for each server
    """
    servers = get_servers()
    if not servers:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'reachable': 0, 'unreachable': 0},
            'error': 'No servers configured'
        })

    private_key = get_private_key()
    if not private_key:
        return json.dumps({
            'results': [],
            'summary': {'total': 0, 'reachable': 0, 'unreachable': 0},
            'error': 'SSH private key not configured'
        })

    username = get_username()
    port = get_port()
    connection_timeout = get_connection_timeout()

    results: List[Dict[str, Any]] = []

    with SSHKeyManager(private_key) as key_path:
        for server in servers:
            try:
                client = paramiko.SSHClient()
                client.set_missing_host_key_policy(_get_host_key_policy())

                private_key_obj = _load_private_key(key_path)

                client.connect(
                    hostname=server,
                    port=port,
                    username=username,
                    pkey=private_key_obj,
                    timeout=connection_timeout,
                    allow_agent=False,
                    look_for_keys=False,
                )

                client.close()

                results.append({
                    'server': server,
                    'reachable': True,
                    'error': None,
                })

            except Exception as e:
                results.append({
                    'server': server,
                    'reachable': False,
                    'error': str(e),
                })

    reachable = sum(1 for r in results if r['reachable'])
    unreachable = len(results) - reachable

    return json.dumps({
        'results': results,
        'summary': {
            'total': len(results),
            'reachable': reachable,
            'unreachable': unreachable,
        }
    })


def get_server_info() -> str:
    """
    Get basic system information from all configured servers.

    Returns:
        JSON string with hostname, OS, and uptime for each server
    """
    info_command = (
        "echo \"HOSTNAME=$(hostname)\" && "
        "echo \"OS=$(cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d'\"' -f2 || uname -s)\" && "
        "echo \"UPTIME=$(uptime -p 2>/dev/null || uptime | awk -F'up ' '{print $2}' | awk -F',' '{print $1}')\""
    )

    result = execute_command(info_command)
    data = json.loads(result)

    info_results: List[Dict[str, Any]] = []

    for server_result in data.get('results', []):
        if server_result['success']:
            info: Dict[str, Any] = {'server': server_result['server']}
            for line in server_result['stdout'].split('\n'):
                if line.startswith('HOSTNAME='):
                    info['hostname'] = line.split('=', 1)[1].strip()
                elif line.startswith('OS='):
                    info['os'] = line.split('=', 1)[1].strip()
                elif line.startswith('UPTIME='):
                    info['uptime'] = line.split('=', 1)[1].strip()
            info['error'] = None
            info_results.append(info)
        else:
            info_results.append({
                'server': server_result['server'],
                'hostname': None,
                'os': None,
                'uptime': None,
                'error': server_result.get('error', 'Command execution failed'),
            })

    return json.dumps({'results': info_results})
