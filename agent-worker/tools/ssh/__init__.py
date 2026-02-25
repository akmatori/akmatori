"""
SSH Tool - Python wrapper for MCP Gateway SSH operations

All credentials are handled by the MCP Gateway.

Usage:
    from ssh import execute_command, test_connectivity, get_server_info

    result = execute_command("uptime", tool_instance_id=3)
    result = test_connectivity(tool_instance_id=3)
    result = get_server_info(tool_instance_id=3)
"""

import sys
import os

# Resolve symlinks so imports work from skill scripts/ dirs
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.realpath(__file__))))

from mcp_client import call


def execute_command(command: str, servers: list = None, tool_instance_id: int = None) -> dict:
    """
    Execute a shell command on configured SSH servers in parallel.

    Args:
        command: The shell command to execute on remote servers
        servers: Optional list of specific servers to target (defaults to all)
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Dictionary with results per server and summary counts
    """
    args = {"command": command}
    if servers:
        args["servers"] = servers
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("ssh.execute_command", args)


def test_connectivity(tool_instance_id: int = None) -> dict:
    """
    Test SSH connectivity to all configured servers.

    Args:
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Dictionary with per-server reachability results
    """
    args = {}
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("ssh.test_connectivity", args)


def get_server_info(servers: list = None, tool_instance_id: int = None) -> dict:
    """
    Get basic system information from configured SSH servers.

    Args:
        servers: Optional list of servers to query
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Dictionary with per-server hostname, OS, uptime
    """
    args = {}
    if servers:
        args["servers"] = servers
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("ssh.get_server_info", args)
