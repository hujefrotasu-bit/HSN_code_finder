# AI HSN Code Finder - Backend

An AI-powered Harmonized System (HS / HSN) classification engine built with Go and PostgreSQL.

## Architecture

1. **In-Memory Official Data:** Loads official UN Comtrade classification records (`HS.json`) on boot.
2. **AI Product Understanding:** Normalizes user queries and extracts technical characteristics (material, form, process, coating) via an OpenAI-compatible endpoint.
3. **In-Memory Candidate Retrieval:** Evaluates token matches, leaf codes, and trade acronyms (`MS`, `SS`, `GI`, `TMT`, `CR`, `HR`, `PVC`) across ~8,000 official codes.
4. **AI Candidate Ranking:** Compares candidates and returns the best matching code, confidence score, detailed reasoning, and clarification questions when necessary.
5. **Deterministic Validation:** Strictly enforces that returned codes exist in the official dataset, have a valid parent, and are not category headers. Falls back automatically if an invalid code is proposed.
6. **PostgreSQL Search History & Caching:** Stores past classifications with explicit token metrics (`input_tokens`, `output_tokens`, `total_tokens`) and cost tracking (`cost_usd`, `cost_inr`).

## Configuration

Copy `.env.example` to `.env` and configure your environment variables:

```bash
cp .env.example .env
```

## Running the Server

```bash
cd backend
go run main.go
```

## PostgreSQL Schema

The database automatically initializes the table `search_history`:

```sql
CREATE TABLE IF NOT EXISTS search_history (
    id SERIAL PRIMARY KEY,
    query VARCHAR(255) NOT NULL,
    hs_code VARCHAR(50) NOT NULL,
    description TEXT,
    chapter VARCHAR(50),
    confidence_score DOUBLE PRECISION,
    reason TEXT,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    cost_usd DOUBLE PRECISION DEFAULT 0.0,
    cost_inr DOUBLE PRECISION DEFAULT 0.0,
    result_json TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

## API Endpoints

### 1. Classify Product
- **POST** `/api/v1/classify`
- **Request Body:**
```json
{
  "query": "MS Plate"
}
```
- **Response:**
```json
{
  "hs_code": "7208",
  "description": "7208 - Iron or non-alloy steel; flat-rolled products of a width of 600mm or more, hot-rolled, not clad, plated or coated",
  "chapter": "72",
  "confidence_score": 0.95,
  "reason": "MS Plate refers to mild steel hot-rolled flat products with width >= 600mm.",
  "alternatives": [
    {"code": "7209", "description": "Flat-rolled products of iron, cold-rolled..."}
  ],
  "questions": [],
  "token_usage": {
    "input_tokens": 542,
    "output_tokens": 128,
    "total_tokens": 670,
    "cost_usd": 0.000158,
    "cost_inr": 0.013675
  }
}
```

### 2. Health Check
- **GET** `/api/v1/health`
