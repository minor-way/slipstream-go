import argparse
import subprocess
import os
import sys
import time
import threading
import socket
import signal
import shutil
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timedelta


def allocate_available_ports(base_port, num_ports):
    """
    Allocate a list of available ports starting from base_port.
    Returns a list of available port numbers.
    Raises RuntimeError if not enough ports are available.
    """
    allocated_ports = []
    current_port = base_port

    while len(allocated_ports) < num_ports:
        if current_port > 65535:
            raise RuntimeError(f"Not enough allocatable ports available. Only allocated {len(allocated_ports)} out of {num_ports} required ports.")

        # Quick check if port is available
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                s.bind(('127.0.0.1', current_port))
                allocated_ports.append(current_port)
        except OSError:
            # Port is in use, try next one
            pass

        current_port += 1

    return allocated_ports


def progress_monitor(stop_event, completed_count, total_count, success_count, progress_lock,
                     start_time, recent_results, results_lock, successful_list):
    """
    Monitor progress with htop-like full-screen display.
    Uses alternate screen buffer for true full-screen like htop/vim.
    """
    import re
    import os

    def strip_ansi(s):
        return re.sub(r'\033\[[0-9;]*m', '', s)

    def pad(text, width):
        """Pad text to width, accounting for ANSI codes."""
        visible = len(strip_ansi(text))
        return text + ' ' * max(0, width - visible)

    # Colors
    G = '\033[92m'   # Green
    R = '\033[91m'   # Red
    C = '\033[96m'   # Cyan
    Y = '\033[93m'   # Yellow
    B = '\033[1m'    # Bold
    N = '\033[0m'    # Reset

    # Enter alternate screen buffer (like htop, vim, less)
    os.system('tput smcup 2>/dev/null || true')
    os.system('tput civis 2>/dev/null || true')

    last_output = ""  # Track last output to avoid unnecessary redraws

    try:
        while not stop_event.wait(0.3):
            term = shutil.get_terminal_size((100, 30))
            W, H = term.columns, term.lines

            # Fixed column widths
            LW = W // 2 - 2
            RW = W - LW - 5

            with progress_lock:
                done = completed_count[0]
                ok = success_count[0]
            fail = done - ok

            with results_lock:
                recent = list(recent_results)
                succs = [e[2] for e in successful_list]

            elapsed = time.time() - start_time
            pct = (done / total_count * 100) if total_count > 0 else 0
            eta = "--:--"
            if done > 0:
                eta = str(timedelta(seconds=int((elapsed / done) * (total_count - done))))
            elapsed_s = str(timedelta(seconds=int(elapsed)))

            # Progress bar
            bw = max(10, LW - 14)
            bf = int(bw * pct / 100)
            bar = '#' * bf + '-' * (bw - bf)

            # Build each line with proper padding
            lines = []

            # Top border
            lines.append(f"{C}+{'-'*LW}+{N} {G}+{'-'*RW}+{N}")

            # Header row
            left = pad(f" {B}SLIPSTREAM TUNNEL CHECKER{N}", LW)
            right = pad(f" {B}SUCCESS (best first){N}", RW)
            lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # Header separator
            lines.append(f"{C}+{'-'*LW}+{N} {G}+{'-'*RW}+{N}")

            # Progress row
            left = pad(f" [{bar}] {pct:5.1f}%", LW)
            right = pad(f" {succs[0][:RW-2]}" if succs else " (waiting...)", RW)
            lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # Stats row
            left = pad(f" {G}OK:{ok}{N} {R}FAIL:{fail}{N} / {total_count}", LW)
            right = pad(f" {succs[1][:RW-2]}" if len(succs) > 1 else "", RW)
            lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # ETA row
            left = pad(f" Elapsed: {elapsed_s}  ETA: {eta}", LW)
            right = pad(f" {succs[2][:RW-2]}" if len(succs) > 2 else "", RW)
            lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # Separator
            right = pad(f" {succs[3][:RW-2]}" if len(succs) > 3 else "", RW)
            lines.append(f"{C}+{'-'*LW}+{N} {G}|{N}{right}{G}|{N}")

            # Recent header
            left = pad(f" {B}Recent Tests:{N}", LW)
            right = pad(f" {succs[4][:RW-2]}" if len(succs) > 4 else "", RW)
            lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # Data rows - fill screen
            data_rows = max(5, H - len(lines) - 1)
            for i in range(data_rows):
                # Left: recent tests
                if i < len(recent):
                    raw = recent[-(i+1)]
                    txt = strip_ansi(raw)[:LW-5]
                    if '✓' in raw:
                        left_content = f" {G}OK{N} {txt}"
                    elif '✗' in raw:
                        left_content = f" {R}X{N}  {txt}"
                    else:
                        left_content = f" {txt}"
                else:
                    left_content = ""

                # Right: successes
                si = 5 + i
                right_content = f" {succs[si][:RW-2]}" if si < len(succs) else ""

                left = pad(left_content, LW)
                right = pad(right_content, RW)
                lines.append(f"{C}|{N}{left}{C}|{N} {G}|{N}{right}{G}|{N}")

            # Bottom border
            lines.append(f"{C}+{'-'*LW}+{N} {G}+{'-'*RW}+{N}")

            # Only redraw if content changed (prevents flickering and selection reset)
            output = '\n'.join(line + '\033[K' for line in lines)
            if output != last_output:
                sys.stdout.write('\033[H')
                sys.stdout.write(output)
                sys.stdout.flush()
                last_output = output

    finally:
        os.system('tput cnorm 2>/dev/null || true')
        os.system('tput rmcup 2>/dev/null || true')


def setup_signal_handler(stop_event, all_processes, process_lock, process_timeout, htop_mode=False):
    """
    Set up signal handler for graceful shutdown on Ctrl+C.
    """
    def signal_handler(signum, frame):
        # Restore terminal if in htop mode
        if htop_mode:
            os.system('tput cnorm 2>/dev/null || true')  # Show cursor
            os.system('tput rmcup 2>/dev/null || true')  # Exit alt screen

        print("\n\nInterrupted! Cleaning up...")
        stop_event.set()

        # Terminate all running slipstream-client processes
        with process_lock:
            for proc in all_processes:
                if proc and proc.poll() is None:
                    try:
                        proc.terminate()
                        proc.wait(timeout=process_timeout)
                    except:
                        try:
                            proc.kill()
                        except:
                            pass

        print("Cleanup complete. Exiting...")
        sys.exit(130)  # Exit code 130 indicates SIGINT termination

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)


def run_speed_test(listen_port, curl_timeout, verbose):
    """
    Run a speed test by downloading a 100KB file.
    Returns speed in KB/sec or None if failed.
    """
    speed_test_url = "http://speed.cloudflare.com/__down?bytes=102400"  # 100KB
    curl_command = [
        "curl",
        "-s",
        "--max-time", str(curl_timeout),
        "-o", "/dev/null",
        "-w", "%{speed_download}",
        "--proxy", f"socks5://127.0.0.1:{listen_port}",
        speed_test_url
    ]

    try:
        result = subprocess.run(curl_command, capture_output=True, text=True, check=False)
        if result.returncode == 0:
            speed_bytes = float(result.stdout.strip())
            speed_kbps = speed_bytes / 1024
            return speed_kbps
    except:
        pass
    return None


def test_single_dns_server(server_info, listen_port, pubkey_file, domain_string, curl_timeout,
                          client_executable, successful_results, results_lock,
                          error_summary, error_lock, verbose, error_log_file,
                          error_log_lock, process_timeout, stop_event, all_processes, process_lock,
                          startup_wait, max_retries, speed_test, recent_results, htop_mode=False,
                          successful_list=None, output_file=None):
    """
    Test a single DNS server by starting slipstream-client and making a curl request.
    Returns True if successful, False otherwise.
    Supports retry logic and optional speed testing.
    """
    # Check if interrupted before starting
    if stop_event.is_set():
        return False

    protocol, address, port = server_info

    # In htop mode, suppress all verbose output - the fixed display handles it
    if htop_mode:
        verbose = False

    # ANSI color codes
    GREEN = '\033[92m'
    RED = '\033[91m'
    YELLOW = '\033[93m'
    RESET = '\033[0m'

    # slipstream-client only supports UDP resolvers
    if protocol != "UDP":
        if verbose:
            print(f"Skipping {protocol} server {address}:{port} - slipstream-client only supports UDP")
        with error_lock:
            error_summary['unsupported_protocol'] = error_summary.get('unsupported_protocol', 0) + 1
        return False

    # Retry loop
    for attempt in range(max_retries + 1):
        if stop_event.is_set():
            return False

        if verbose:
            retry_str = f" (retry {attempt})" if attempt > 0 else ""
            print(f"\n--- Testing UDP resolver: {address}:{port}{retry_str} ---")

        # Build slipstream-client command
        client_command = [
            client_executable,
            "--domain", domain_string,
            "--resolver", f"{address}:{port}",
            "--listen", f"127.0.0.1:{listen_port}",
            "--pubkey-file", pubkey_file,
            "--log-level", "error"  # Reduce noise during testing
        ]

        client_process = None
        error_type = None
        success = False

        try:
            # Start slipstream-client in a non-blocking way
            client_process = subprocess.Popen(
                client_command,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )

            # Track process for cleanup
            with process_lock:
                all_processes.append(client_process)

            # Give slipstream-client time to establish QUIC tunnel
            time.sleep(startup_wait)

            # Check if process is still running
            if client_process.poll() is not None:
                error_type = "client_startup_failed"
                if verbose:
                    stderr_output = client_process.stderr.read() if client_process.stderr else ""
                    print(f"{RED}slipstream-client exited early: {stderr_output}{RESET}")
                continue  # Retry

            if verbose:
                print("Making connectivity test...")

            # Connectivity test
            curl_command = [
                "curl",
                "-s",
                "--max-time", str(curl_timeout),
                "-i",
                "--proxy", f"socks5://127.0.0.1:{listen_port}",
                "http://www.gstatic.com/generate_204"
            ]

            curl_start_time = time.time()
            curl_result = subprocess.run(curl_command, capture_output=True, text=True, check=False)
            curl_elapsed_time = time.time() - curl_start_time

            if verbose:
                print("\n----- Curl Output -----")
                print(curl_result.stdout)
                print(curl_result.stderr)
                print("-----------------------")

            if curl_result.returncode == 0:
                # Connectivity test passed
                speed_kbps = None

                # Optional speed test
                if speed_test:
                    if verbose:
                        print("Running speed test (100KB download)...")
                    speed_kbps = run_speed_test(listen_port, curl_timeout, verbose)
                    if verbose:
                        if speed_kbps:
                            print(f"Speed: {speed_kbps:.1f} KB/sec")
                        else:
                            print(f"{YELLOW}Speed test failed{RESET}")

                # Format result string
                if speed_kbps:
                    result_str = f"\033[92m✓\033[0m {protocol}: {address}:{port} {curl_elapsed_time:.2f}s {speed_kbps:.1f}KB/s"
                else:
                    result_str = f"\033[92m✓\033[0m {protocol}: {address}:{port} {curl_elapsed_time:.2f}s"

                if verbose:
                    print(result_str)

                # Store successful result
                with results_lock:
                    successful_results.append((protocol, address, port, curl_elapsed_time, speed_kbps))
                    recent_results.append(result_str)
                    # Keep last 50 results for display
                    while len(recent_results) > 50:
                        recent_results.pop(0)
                    # Add to successful_list for right panel, sorted by best performance
                    if successful_list is not None:
                        # Always show both latency and speed (or N/A if no speed test)
                        if speed_kbps:
                            entry = (speed_kbps, curl_elapsed_time, f"{address}:{port} {curl_elapsed_time:.1f}s {speed_kbps:.0f}KB/s")
                        else:
                            entry = (0, curl_elapsed_time, f"{address}:{port} {curl_elapsed_time:.1f}s --KB/s")
                        successful_list.append(entry)
                        # Sort: highest speed first, then lowest latency
                        successful_list.sort(key=lambda x: (-x[0], x[1]))

                    # Write to output file immediately (real-time saving)
                    if output_file:
                        try:
                            with open(output_file, 'w') as f:
                                for res in successful_results:
                                    p, a, pt, lat = res[:4]
                                    spd = res[4] if len(res) > 4 else None
                                    if spd is not None:
                                        f.write(f"{p}: {a}:{pt} {lat:.2f}s {spd:.1f}KB/s\n")
                                    else:
                                        f.write(f"{p}: {a}:{pt} {lat:.2f}s\n")
                        except:
                            pass  # Ignore write errors in background

                success = True
                break  # Success, no need to retry

            else:
                # Categorize the error
                if curl_result.returncode == 28:
                    error_type = "curl_timeout"
                elif "Connection refused" in curl_result.stderr or "Failed to connect" in curl_result.stderr:
                    error_type = "connection_refused"
                elif "Could not resolve proxy" in curl_result.stderr:
                    error_type = "proxy_error"
                else:
                    error_type = f"curl_failed_code_{curl_result.returncode}"

                if verbose:
                    print(f"{RED}FAILED: {address}:{port} - {error_type}{RESET}")

        except FileNotFoundError:
            error_type = "curl_not_found"
            if verbose:
                print("Error: 'curl' command not found.")
        except Exception as e:
            error_type = "exception"
            if verbose:
                print(f"Error: {e}")
        finally:
            if client_process:
                # Remove from tracking list
                with process_lock:
                    try:
                        all_processes.remove(client_process)
                    except ValueError:
                        pass

                # Terminate if still running
                if client_process.poll() is None:
                    if verbose:
                        print(f"Terminating slipstream-client...")
                    client_process.terminate()
                    try:
                        client_process.wait(timeout=process_timeout)
                    except subprocess.TimeoutExpired:
                        client_process.kill()

        # If not the last attempt, wait before retry
        if attempt < max_retries and not success:
            if verbose:
                print(f"{YELLOW}Retrying in 2 seconds...{RESET}")
            time.sleep(2)

    # Update error summary if all attempts failed
    if not success and error_type:
        with error_lock:
            error_summary[error_type] = error_summary.get(error_type, 0) + 1

        # Add to recent results
        with results_lock:
            fail_str = f"\033[91m✗\033[0m {protocol}: {address}:{port} {error_type}"
            recent_results.append(fail_str)
            while len(recent_results) > 50:
                recent_results.pop(0)

        # Write to error log if specified
        if error_log_file:
            try:
                with error_log_lock:
                    with open(error_log_file, 'a') as f:
                        f.write(f"{protocol}: {address}:{port} | {error_type}\n")
            except Exception as e:
                if verbose:
                    print(f"{RED}Error writing to error log: {e}{RESET}")

    return success


def print_summary(total_count, success_count, error_summary, error_log_file, successful_results, speed_test):
    """
    Print a summary of the test results.
    """
    failed_count = total_count - success_count

    print("\n" + "="*60)
    print("SUMMARY")
    print("="*60)
    print(f"Completed: {success_count}/{total_count} successful")

    # Show speed statistics if speed test was enabled
    if speed_test and successful_results:
        speeds = [r[4] for r in successful_results if r[4] is not None]
        if speeds:
            avg_speed = sum(speeds) / len(speeds)
            max_speed = max(speeds)
            min_speed = min(speeds)
            print(f"\nSpeed Statistics:")
            print(f"  - Average: {avg_speed:.1f} KB/sec")
            print(f"  - Best:    {max_speed:.1f} KB/sec")
            print(f"  - Worst:   {min_speed:.1f} KB/sec")

    if failed_count > 0:
        print(f"\nErrors ({failed_count} total):")
        # Sort by frequency (descending)
        sorted_errors = sorted(error_summary.items(), key=lambda x: x[1], reverse=True)
        for error_type, count in sorted_errors:
            print(f"  - {count} {error_type}")

    if error_log_file:
        print(f"\nError log written to: {error_log_file}")

    print("="*60)


def run_slipstream_client_and_curl(dns_servers_file, pubkey_file, domain_string, listen_port,
                                    curl_timeout, output_file, workers, verbose, error_log_file,
                                    process_timeout, startup_wait, max_retries, speed_test):
    """
    Loops through DNS servers, starts slipstream-client, and makes a curl request.
    Supports multi-threaded execution for faster testing.
    """
    client_executable = os.path.join(os.path.dirname(__file__), "slipstream-client")

    if not os.path.exists(client_executable):
        print(f"Error: slipstream-client executable not found at {client_executable}")
        print("Build it with: go build -o slipstream-client ./cmd/client")
        return

    if not os.path.exists(pubkey_file):
        print(f"Error: Public key file not found at {pubkey_file}")
        return

    # Read and parse DNS servers
    try:
        with open(dns_servers_file, 'r') as f:
            dns_servers = []
            for line in f:
                line = line.strip()
                if not line or line.startswith('#'):
                    continue
                # Parse format: protocol: address:port
                if ':' in line:
                    parts = line.split(':', 1)
                    if len(parts) == 2:
                        protocol = parts[0].strip().upper()
                        server_part = parts[1].strip()
                        # server_part is "address:port"
                        if ':' in server_part:
                            address, port = server_part.rsplit(':', 1)
                            try:
                                port = int(port)
                                dns_servers.append((protocol, address, port))
                            except ValueError:
                                print(f"Warning: Invalid port in line: {line}")
                        else:
                            print(f"Warning: Invalid format in line: {line}")
                else:
                    print(f"Warning: Invalid format in line: {line}")
    except FileNotFoundError:
        print(f"Error: DNS servers file not found at {dns_servers_file}")
        return
    except Exception as e:
        print(f"Error reading DNS servers file: {e}")
        return

    if not dns_servers:
        print("No DNS servers found in the file.")
        return

    # Filter to only UDP servers (slipstream-client only supports UDP)
    udp_servers = [s for s in dns_servers if s[0] == "UDP"]
    skipped_count = len(dns_servers) - len(udp_servers)

    if skipped_count > 0:
        print(f"Note: Skipping {skipped_count} non-UDP servers (slipstream-client only supports UDP resolvers)")

    if not udp_servers:
        print("No UDP DNS servers found in the file. slipstream-client only supports UDP resolvers.")
        return

    total_count = len(udp_servers)
    print(f"Found {total_count} UDP DNS servers to test.")

    # Allocate ports for workers
    try:
        allocated_ports = allocate_available_ports(listen_port, workers)
        print(f"Allocated ports: {allocated_ports[0]}-{allocated_ports[-1]}")
    except RuntimeError as e:
        print(f"Error: {e}")
        return

    # Initialize shared state
    results_lock = threading.Lock()
    error_lock = threading.Lock()
    progress_lock = threading.Lock()
    process_lock = threading.Lock()
    error_log_lock = threading.Lock()

    completed_count = [0]  # Use list to make it mutable in nested scope
    success_count = [0]
    error_summary = {}
    all_processes = []
    successful_results = []  # Store results for sorting by latency
    recent_results = []  # Store recent results for display
    successful_list = []  # Store successful IPs for right panel display

    # Set up signal handler for graceful shutdown
    stop_event = threading.Event()
    htop_mode = workers > 1
    setup_signal_handler(stop_event, all_processes, process_lock, process_timeout, htop_mode)

    # Record start time for ETA calculation
    start_time = time.time()

    # Start progress monitor thread if using multiple workers
    progress_thread = None
    if workers > 1:
        progress_thread = threading.Thread(
            target=progress_monitor,
            args=(stop_event, completed_count, total_count, success_count, progress_lock,
                  start_time, recent_results, results_lock, successful_list),
            daemon=True
        )
        progress_thread.start()

    # Use ThreadPoolExecutor for concurrent testing
    with ThreadPoolExecutor(max_workers=workers) as executor:
        # Submit all tasks
        future_to_server = {}
        for idx, server_info in enumerate(udp_servers):
            # Check if interrupted before submitting more tasks
            if stop_event.is_set():
                break

            assigned_port = allocated_ports[idx % workers]
            # htop_mode=True when workers > 1 to suppress verbose output
            htop_mode = workers > 1
            future = executor.submit(
                test_single_dns_server,
                server_info,
                assigned_port,
                pubkey_file,
                domain_string,
                curl_timeout,
                client_executable,
                successful_results,
                results_lock,
                error_summary,
                error_lock,
                verbose,
                error_log_file,
                error_log_lock,
                process_timeout,
                stop_event,
                all_processes,
                process_lock,
                startup_wait,
                max_retries,
                speed_test,
                recent_results,
                htop_mode,
                successful_list,
                output_file
            )
            future_to_server[future] = server_info

        # Process completed tasks
        for future in as_completed(future_to_server):
            if stop_event.is_set():
                break

            try:
                success = future.result()
                with progress_lock:
                    completed_count[0] += 1
                    if success:
                        success_count[0] += 1
            except Exception as e:
                with progress_lock:
                    completed_count[0] += 1
                if verbose:
                    server_info = future_to_server[future]
                    print(f"Exception occurred while testing {server_info}: {e}")

    # Signal progress thread to stop
    stop_event.set()
    if progress_thread:
        progress_thread.join(timeout=2)
        # Alternate screen buffer exit is handled by progress_monitor's finally block
        # Original terminal content is automatically restored

    # Sort results by latency (lowest first) or by speed (highest first) if speed test enabled
    if speed_test:
        # Sort by speed (highest first), with None values at the end
        successful_results.sort(key=lambda x: x[4] if x[4] is not None else 0, reverse=True)
    else:
        successful_results.sort(key=lambda x: x[3])  # Sort by curl_elapsed_time

    try:
        with open(output_file, 'w') as f:
            for result in successful_results:
                protocol, address, port, latency = result[:4]
                speed_kbps = result[4] if len(result) > 4 else None
                if speed_kbps is not None:
                    f.write(f"{protocol}: {address}:{port} {latency:.2f}s {speed_kbps:.1f}KB/s\n")
                else:
                    f.write(f"{protocol}: {address}:{port} {latency:.2f}s\n")
        sort_type = "highest speed" if speed_test else "lowest latency"
        print(f"\nResults written to {output_file} (sorted by {sort_type})")
    except Exception as e:
        print(f"\nError writing results to {output_file}: {e}")

    # Print final summary
    print_summary(total_count, success_count[0], error_summary, error_log_file, successful_results, speed_test)


def main():
    parser = argparse.ArgumentParser(
        description="Test DNS resolvers for slipstream tunnel connectivity."
    )
    parser.add_argument(
        "dns_servers_file",
        help="Path to the file containing DNS servers (format: 'UDP: address:port')."
    )
    parser.add_argument(
        "pubkey_file",
        help="Path to the server public key file for slipstream-client."
    )
    parser.add_argument(
        "domain_string",
        help="The tunnel domain for slipstream-client (e.g., f.psvm.ir)."
    )
    parser.add_argument(
        "--listen_port",
        type=int,
        default=56345,
        help="The base local SOCKS5 port (default: 56345)."
    )
    parser.add_argument(
        "--curl_timeout",
        type=int,
        default=30,
        help="The curl request timeout in seconds (default: 30)."
    )
    parser.add_argument(
        "--output",
        "-o",
        type=str,
        default="tunnel_check_successful.txt",
        help="Output file for successful DNS servers (default: tunnel_check_successful.txt)."
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=5,
        help="Number of concurrent worker threads (default: 5, max: 20)."
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Enable verbose output showing details for each test."
    )
    parser.add_argument(
        "--error-log",
        type=str,
        default=None,
        help="Optional file to log failed DNS servers with error types."
    )
    parser.add_argument(
        "--process-timeout",
        type=int,
        default=5,
        help="Timeout in seconds for terminating slipstream-client processes (default: 5)."
    )
    parser.add_argument(
        "--startup-wait",
        type=int,
        default=5,
        help="Seconds to wait for QUIC tunnel establishment before testing (default: 5)."
    )
    parser.add_argument(
        "--retries",
        type=int,
        default=1,
        help="Number of retries for failed resolvers (default: 1)."
    )
    parser.add_argument(
        "--speed-test",
        action="store_true",
        help="Run speed test (100KB download) for each successful resolver."
    )

    args = parser.parse_args()

    # Validate workers count (lower max due to QUIC overhead)
    if args.workers < 1 or args.workers > 20:
        parser.error("--workers must be between 1 and 20")

    # Enable verbose by default for single-threaded mode (backward compatibility)
    if args.workers == 1 and not args.verbose:
        args.verbose = True

    run_slipstream_client_and_curl(
        args.dns_servers_file,
        args.pubkey_file,
        args.domain_string,
        args.listen_port,
        args.curl_timeout,
        args.output,
        args.workers,
        args.verbose,
        args.error_log,
        args.process_timeout,
        args.startup_wait,
        args.retries,
        args.speed_test
    )

if __name__ == "__main__":
    main()
