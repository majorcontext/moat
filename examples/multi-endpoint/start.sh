#!/bin/bash
# Start both servers in the background

python3 /workspace/web_server.py &
python3 /workspace/api_server.py &

echo "Servers started. Press Ctrl+C to stop."

# Wait for any process to exit
wait -n

# Exit with status of process that exited first
exit $?
