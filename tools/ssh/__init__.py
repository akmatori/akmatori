"""
SSH Tool - Remote Command Execution

This tool provides SSH command execution capabilities for Akmatori.
Execute commands across multiple servers in parallel with aggregated results.

Example:
    from ssh import execute_command, test_connectivity

    # Test connectivity first
    connectivity = test_connectivity()

    # Execute a command on all servers
    result = execute_command("uptime")
"""

from .config import get_config, set_config, reset_config

from .ssh import (
    execute_command,
    execute_on_servers,
    test_connectivity,
    get_server_info,
)

__all__ = [
    # Configuration
    'get_config',
    'set_config',
    'reset_config',

    # Functions
    'execute_command',
    'execute_on_servers',
    'test_connectivity',
    'get_server_info',
]
