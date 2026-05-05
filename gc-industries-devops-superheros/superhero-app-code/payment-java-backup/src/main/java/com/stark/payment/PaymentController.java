package com.stark.payment.controllers;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.Map;

@RestController
public class PaymentController {

    @GetMapping("/health")
    public Map<String, String> health() {
        return Map.of(
            "service", "payment",
            "status", "ok"
        );
    }

    @GetMapping("/pay")
    public Map<String, String> pay() {
        return Map.of(
            "status", "ok",
            "message", "Payment successful"
        );
    }
}
