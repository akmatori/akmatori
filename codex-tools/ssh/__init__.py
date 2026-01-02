"""
SSH Tool - Thin MCP wrapper for SSH operations

This module provides SSH command execution on remote servers.
All credentials are handled by the MCP Gateway.

Example usage:
    from tools.ssh import execute_command, test_connectivity, get_server_info

    # Execute a command on all configured servers
    result = execute_command("uptime")
    for server_result in result['results']:
        print(f"{server_result['server']}: {server_result['stdout']}")

    # Execute on specific servers only
    result = execute_command("df -h", servers=["server1", "server2"])

    # Test connectivity to all servers
    connectivity = test_connectivity()
    for r in connectivity['results']:
        status = "OK" if r['reachable'] else f"FAILED: {r['error']}"
        print(f"{r['server']}: {status}")

    # Get system information from all servers
    info = get_server_info()
"""

import sys
import os

# Add parent directory to path for mcp_client import
# Use realpath to resolve symlinks (tools are symlinked from skills/*/scripts/)
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.realpath(__file__))))

from mcp_client import call


def execute_command(command: str, servers: list = None) -> dict:
    """
    Execute a shell command on configured SSH servers in parallel.

    Args:
        command: The shell command to execute on remote servers
        servers: Optional list of specific servers to target (defaults to all)

    Returns:
        Dictionary with:
            - results: List of per-server results, each containing:
                - server: Server hostname/IP
                - success: Whether command succeeded (exit code 0)
                - stdout: Standard output
                - stderr: Standard error
                - exit_code: Command exit code
                - duration_ms: Execution time in milliseconds
                - error: Error message if failed
            - summary: Aggregated counts (total, succeeded, failed)

    Example:
        result = execute_command("uptime")
        print(f"Succeeded: {result['summary']['succeeded']}/{result['summary']['total']}")
        for r in result['results']:
            if r['success']:
                print(f"{r['server']}: {r['stdout'].strip()}")
    """
    args = {"command": command}
    if servers:
        args["servers"] = servers
    return call("ssh.execute_command", args)


def execute_on_servers(command: str, servers: list) -> dict:
    """
    Execute a command on specific servers only.

    Args:
        command: The shell command to execute
        servers: List of server hostnames/IPs to target

    Returns:
        Same as execute_command()
    """
    return execute_command(command, servers=servers)


def test_connectivity() -> dict:
    """
    Test SSH connectivity to all configured servers.

    Returns:
        Dictionary with:
            - results: List of per-server results, each containing:
                - server: Server hostname/IP
                - reachable: Whether SSH connection succeeded
                - error: Error message if unreachable
            - summary: Aggregated counts (total, reachable, unreachable)

    Example:
        result = test_connectivity()
        for r in result['results']:
            status = "OK" if r['reachable'] else f"FAILED: {r['error']}"
            print(f"{r['server']}: {status}")
    """
    return call("ssh.test_connectivity")


def get_server_info() -> dict:
    """
    Get basic system information from all configured servers.

    Retrieves hostname, OS, and uptime from each server.

    Returns:
        Dictionary with:
            - results: List of per-server results, each containing:
                - server: Server hostname/IP
                - hostname: System hostname
                - os: Operating system name
                - uptime: System uptime
                - error: Error message if failed

    Example:
        result = get_server_info()
        for r in result['results']:
            if not r.get('error'):
                print(f"{r['server']}: {r['os']} - up {r['uptime']}")
    """
    return call("ssh.get_server_info")
