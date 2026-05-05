const express = require("express");
const cors = require("cors");

const app = express();
const PORT = 8082;

app.use(cors());
app.use(express.json());

app.get("/health", (req, res) => {
  res.json({ service: "orders", status: "ok" });
});

app.post("/", (req, res) => {
  res.json({
    status: "ok",
    message: "Order created",
    order: req.body
  });
});

app.listen(PORT, () =>
  console.log(`🔥 Orders running on ${PORT}`)
);


// test comment for pipeline
// another test comment for pipeline