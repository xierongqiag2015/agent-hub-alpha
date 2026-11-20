package com.openjiuwen.demo.w06;

import com.openjiuwen.service.spec.spi.AgentHandler;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

@SpringBootApplication
public class W06AgentApplication {
    public static void main(String[] args) { SpringApplication.run(W06AgentApplication.class, args); }
    @Bean AgentHandler w06RoleHandler() { return new W06RoleHandler(System.getenv()); }
}
