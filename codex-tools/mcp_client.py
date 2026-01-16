"""
MCP Client Library for Codex Tools

This module provides a client for communicating with the MCP Gateway.
All tool calls are routed through the MCP Gateway which handles
authentication and credential management.
"""

import json
import os
import urllib.error
import urllib.request
from typing import Any, Dict, Optional


class MCPError(Exception):
    """Exception raised for MCP protocol errors"""
    def __init__(self, code: int, message: str, data: Any = None):
        self.code = code
        self.message = message
        self.data = data
        super().__init__(f"MCP Error {code}: {message}")


class MCPClient:
    """Client for communicating with MCP Gateway"""

    def __init__(self, gateway_url: Optional[str] = None, incident_id: Optional[str] = None):
        """
        Initialize MCP Client.

        Args:
            gateway_url: URL of the MCP Gateway (defaults to MCP_GATEWAY_URL env var)
            incident_id: Incident ID for credential resolution (defaults to INCIDENT_ID env var)
        """
        self.gateway_url = gateway_url or os.environ.get("MCP_GATEWAY_URL", "http://mcp-gateway:8080")
        self.incident_id = incident_id or os.environ.get("INCIDENT_ID", "")
        self._request_id = 0

        # Create an opener that bypasses proxy for internal MCP gateway connections
        # This prevents HTTP_PROXY/HTTPS_PROXY env vars from affecting internal traffic
        no_proxy_handler = urllib.request.ProxyHandler({})
        self._opener = urllib.request.build_opener(no_proxy_handler)

    def _next_request_id(self) -> int:
        """Get the next request ID"""
        self._request_id += 1
        return self._request_id

    def call(self, tool_name: str, arguments: Optional[Dict[str, Any]] = None) -> Any:
        """
        Call an MCP tool.

        Args:
            tool_name: Name of the tool (e.g., "ssh.execute_command", "zabbix.get_hosts")
            arguments: Arguments to pass to the tool

        Returns:
            The tool's result (parsed JSON if applicable)

        Raises:
            MCPError: If the MCP call fails
        """
        if arguments is None:
            arguments = {}

        # Build JSON-RPC request
        request = {
            "jsonrpc": "2.0",
            "method": "tools/call",
            "params": {
                "name": tool_name,
                "arguments": arguments
            },
            "id": self._next_request_id()
        }

        url = f"{self.gateway_url.rstrip('/')}/mcp"
        payload = json.dumps(request).encode("utf-8")

        headers = {
            "Content-Type": "application/json",
        }

        # Add incident ID if available
        if self.incident_id:
            headers["X-Incident-ID"] = self.incident_id

        req = urllib.request.Request(url, data=payload, headers=headers, method="POST")

        try:
            # Use our custom opener that bypasses proxy for internal connections
            with self._opener.open(req, timeout=300) as response:
                resp_data = response.read()
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="ignore")
            raise MCPError(-32000, f"HTTP error {exc.code}: {body}")
        except urllib.error.URLError as exc:
            raise MCPError(-32000, f"Connection error: {exc}")
        except Exception as exc:
            raise MCPError(-32000, f"Request failed: {exc}")

        try:
            result = json.loads(resp_data)
        except json.JSONDecodeError as exc:
            raise MCPError(-32700, f"Invalid JSON response: {exc}")

        # Check for JSON-RPC error
        if "error" in result:
            error = result["error"]
            raise MCPError(
                error.get("code", -32000),
                error.get("message", "Unknown error"),
                error.get("data")
            )

        # Extract result content
        if "result" in result:
            content = result["result"]

            # Handle CallToolResult format
            if isinstance(content, dict) and "content" in content:
                contents = content["content"]
                if isinstance(contents, list) and len(contents) > 0:
                    text = contents[0].get("text", "")
                    # Try to parse as JSON
                    try:
                        return json.loads(text)
                    except json.JSONDecodeError:
                        return text

                # Check for error
                if content.get("isError"):
                    raise MCPError(-32000, "Tool execution failed", contents)

            return content

        return None

    def list_tools(self) -> list:
        """
        List available tools from the MCP Gateway.

        Returns:
            List of tool definitions
        """
        request = {
            "jsonrpc": "2.0",
            "method": "tools/list",
            "params": {},
            "id": self._next_request_id()
        }

        url = f"{self.gateway_url.rstrip('/')}/mcp"
        payload = json.dumps(request).encode("utf-8")

        headers = {"Content-Type": "application/json"}
        req = urllib.request.Request(url, data=payload, headers=headers, method="POST")

        try:
            # Use our custom opener that bypasses proxy for internal connections
            with self._opener.open(req, timeout=30) as response:
                resp_data = response.read()
        except Exception as exc:
            raise MCPError(-32000, f"Failed to list tools: {exc}")

        result = json.loads(resp_data)

        if "error" in result:
            error = result["error"]
            raise MCPError(error.get("code", -32000), error.get("message", "Unknown error"))

        if "result" in result and "tools" in result["result"]:
            return result["result"]["tools"]

        return []


# Global client instance (lazy initialized)
_client: Optional[MCPClient] = None


def get_client() -> MCPClient:
    """Get or create the global MCP client instance"""
    global _client
    if _client is None:
        _client = MCPClient()
    return _client


def call(tool_name: str, arguments: Optional[Dict[str, Any]] = None) -> Any:
    """
    Convenience function to call an MCP tool.

    Args:
        tool_name: Name of the tool
        arguments: Arguments to pass to the tool

    Returns:
        The tool's result
    """
    return get_client().call(tool_name, arguments)


def list_tools() -> list:
    """
    Convenience function to list available tools.

    Returns:
        List of tool definitions
    """
    return get_client().list_tools()
