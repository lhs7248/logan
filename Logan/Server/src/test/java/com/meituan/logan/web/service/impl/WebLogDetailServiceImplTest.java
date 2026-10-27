package com.meituan.logan.web.service.impl;

import com.meituan.logan.web.dto.WebLogDetailDTO;
import com.meituan.logan.web.mapper.WebLogDetailMapper;
import com.meituan.logan.web.task.TaskLoop;
import org.junit.Test;
import org.springframework.transaction.support.AbstractPlatformTransactionManager;
import org.springframework.transaction.support.DefaultTransactionStatus;
import org.springframework.transaction.TransactionDefinition;
import java.lang.reflect.Field;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import static org.junit.Assert.*;

public class WebLogDetailServiceImplTest {
    private static class Transactions extends AbstractPlatformTransactionManager {
        int commits, rollbacks;
        protected Object doGetTransaction() { return new Object(); }
        protected void doBegin(Object tx, TransactionDefinition definition) {}
        protected void doCommit(DefaultTransactionStatus status) { commits++; }
        protected void doRollback(DefaultTransactionStatus status) { rollbacks++; }
    }

    private WebLogDetailServiceImpl service(List<Integer> batches, Transactions tx, boolean fail) throws Exception {
        WebLogDetailServiceImpl service = new WebLogDetailServiceImpl();
        Field mapper = WebLogDetailServiceImpl.class.getDeclaredField("detailMapper");
        mapper.setAccessible(true);
        mapper.set(service, Proxy.newProxyInstance(getClass().getClassLoader(), new Class[]{WebLogDetailMapper.class},
                (proxy, method, args) -> {
                    if (method.getName().equals("batchInsert")) {
                        batches.add(((List<?>) args[0]).size());
                        if (fail && batches.size() == 2) throw new IllegalStateException("test insert failure");
                    }
                    return null;
                }));
        // A delayed queue deterministically reproduces the old return-before-write behavior.
        Field loop = AbstractBatchInsertService.class.getDeclaredField("taskLoop");
        loop.setAccessible(true);
        loop.set(service, Proxy.newProxyInstance(getClass().getClassLoader(), new Class[]{TaskLoop.class},
                (proxy, method, args) -> null));
        try {
            Field manager = WebLogDetailServiceImpl.class.getDeclaredField("transactionManager");
            manager.setAccessible(true);
            manager.set(service, tx);
        } catch (NoSuchFieldException beforeFix) {
            // Allows this same behavioral test to reproduce the pre-fix race.
        }
        return service;
    }

    @Test public void returnsOnlyAfterAllBatchesCommit() throws Exception {
        List<Integer> batches = new ArrayList<>();
        Transactions tx = new Transactions();
        service(batches, tx, false).saveLogDetails(Collections.nCopies(201, new WebLogDetailDTO()));
        assertEquals(java.util.Arrays.asList(200, 1), batches);
        assertEquals(1, tx.commits);
        assertEquals(0, tx.rollbacks);
    }

    @Test public void failurePropagatesAndRollsBack() throws Exception {
        List<Integer> batches = new ArrayList<>();
        Transactions tx = new Transactions();
        try {
            service(batches, tx, true).saveLogDetails(Collections.nCopies(201, new WebLogDetailDTO()));
            fail("failed insert must prevent the caller from marking the task analyzed");
        } catch (IllegalStateException expected) {
            assertEquals("test insert failure", expected.getMessage());
        }
        assertEquals(0, tx.commits);
        assertEquals(1, tx.rollbacks);
    }

    @Test public void emptyInputDoesNotWrite() throws Exception {
        List<Integer> batches = new ArrayList<>();
        Transactions tx = new Transactions();
        service(batches, tx, false).saveLogDetails(Collections.emptyList());
        assertTrue(batches.isEmpty());
        assertEquals(0, tx.commits);
    }
}
