"""
Problem Management Tools for Zabbix

Example:
    from lib.zabbix_py.problem import problem_get

    # Get recent problems
    problems = problem_get({'recent': True, 'limit': 10})

    # Get problems for specific host
    host_problems = problem_get({'hostids': ['10001']})
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat
from .utils import zabbix_request


class ProblemGetParams(TypedDict, total=False):
    """Parameters for problem.get API method"""
    eventids: List[str]
    groupids: List[str]
    hostids: List[str]
    objectids: List[str]
    output: OutputFormat
    time_from: int
    time_till: int
    recent: bool
    severities: List[int]
    limit: int


def problem_get(params: Optional[ProblemGetParams] = None) -> str:
    """
    Get problems from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of problems
    """
    if params is None:
        params = {}
    return zabbix_request('problem.get', params)
