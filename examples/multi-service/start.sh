#!/bin/bash
# Start both servers in the background

python3 /workspace/web_server.py &
python3 /workspace/api_server.py &

echo "Both servers started. Press Ctrl+C to stop."
echo ""
echo "Services available at:"
echo "  Web: ${AGENTOPS_URL_WEB:-http://localhost:3000}"
echo "  API: ${AGENTOPS_URL_API:-http://localhost:8080}"
echo ""

# Wait for any process to exit
wait -n

# Exit with status of process that exited first
exit $?
