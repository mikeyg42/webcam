#!/bin/bash
# Setup script for MinIO and PostgreSQL storage infrastructure

set -e  # Exit on error

echo "🚀 Setting up storage infrastructure for webcam2..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${RED}✗ Docker is not running. Please start Docker and try again.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Docker is running${NC}"

# Start Docker Compose services
echo ""
echo "📦 Starting Docker services..."
docker compose up -d postgres minio

# Wait for PostgreSQL to be ready
echo ""
echo "⏳ Waiting for PostgreSQL to be ready..."
MAX_RETRIES=30
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if docker compose exec -T postgres pg_isready -U recorder -d recordings > /dev/null 2>&1; then
        echo -e "${GREEN}✓ PostgreSQL is ready${NC}"
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT + 1))
    echo "  Attempt $RETRY_COUNT/$MAX_RETRIES..."
    sleep 1
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
    echo -e "${RED}✗ PostgreSQL failed to start${NC}"
    docker compose logs postgres
    exit 1
fi

# Wait for MinIO to be ready
echo ""
echo "⏳ Waiting for MinIO to be ready..."
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if curl -sf http://localhost:9000/minio/health/live > /dev/null 2>&1; then
        echo -e "${GREEN}✓ MinIO is ready${NC}"
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT + 1))
    echo "  Attempt $RETRY_COUNT/$MAX_RETRIES..."
    sleep 1
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
    echo -e "${RED}✗ MinIO failed to start${NC}"
    docker compose logs minio
    exit 1
fi

# Run MinIO setup (create bucket)
echo ""
echo "🪣 Creating MinIO bucket..."
docker compose up minio-setup

# Verify PostgreSQL schema
echo ""
echo "🗄️  Verifying PostgreSQL schema..."
TABLE_COUNT=$(docker compose exec -T postgres psql -U recorder -d recordings -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE';" | tr -d ' ')

if [ "$TABLE_COUNT" -ge 3 ]; then
    echo -e "${GREEN}✓ Database schema created successfully ($TABLE_COUNT tables)${NC}"
else
    echo -e "${YELLOW}⚠ Expected at least 3 tables, found $TABLE_COUNT${NC}"
fi

# Verify MinIO bucket
echo ""
echo "🪣 Verifying MinIO bucket..."
if docker exec webcam2-minio mc ls myminio/recordings > /dev/null 2>&1; then
    echo -e "${GREEN}✓ MinIO bucket 'recordings' exists${NC}"
else
    echo -e "${YELLOW}⚠ MinIO bucket verification skipped${NC}"
fi

# Show connection details
echo ""
echo "═══════════════════════════════════════════════════════════"
echo -e "${GREEN}✅ Storage infrastructure is ready!${NC}"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "📊 PostgreSQL:"
echo "   Host: localhost:5432"
echo "   Database: recordings"
echo "   User: recorder"
echo "   Password: recorder"
echo "   DSN: postgres://recorder:recorder@localhost:5432/recordings?sslmode=disable"
echo ""
echo "🗄️  MinIO:"
echo "   API: http://localhost:9000"
echo "   Console: http://localhost:9001"
echo "   User: minioadmin"
echo "   Password: minioadmin"
echo "   Bucket: recordings"
echo ""
echo "🧪 Test connections:"
echo "   psql postgres://recorder:recorder@localhost:5432/recordings -c 'SELECT 1'"
echo "   curl http://localhost:9000/minio/health/live"
echo ""
echo "📝 View logs:"
echo "   docker compose logs -f postgres"
echo "   docker compose logs -f minio"
echo ""
echo "🛑 Stop services:"
echo "   docker compose down"
echo ""
echo "🗑️  Reset data:"
echo "   docker compose down -v  # WARNING: Deletes all data!"
echo "═══════════════════════════════════════════════════════════"
